package app

import (
	"context"
	"time"

	"github.com/omni-network/omni/contracts/bindings"
	"github.com/omni-network/omni/e2e/app/eoa"
	"github.com/omni-network/omni/e2e/netman"
	"github.com/omni-network/omni/e2e/netman/pingpong"
	"github.com/omni-network/omni/e2e/types"
	"github.com/omni-network/omni/halo/app/upgrades/static"
	"github.com/omni-network/omni/halo/genutil/evm/predeploys"
	"github.com/omni-network/omni/lib/cchain/provider"
	"github.com/omni-network/omni/lib/cchain/queryutil"
	"github.com/omni-network/omni/lib/contracts"
	"github.com/omni-network/omni/lib/errors"
	"github.com/omni-network/omni/lib/k1util"
	"github.com/omni-network/omni/lib/log"
	"github.com/omni-network/omni/lib/netconf"
	"github.com/omni-network/omni/lib/umath"

	e2e "github.com/cometbft/cometbft/test/e2e/pkg"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"

	"golang.org/x/sync/errgroup"
)

const (
	// defaultPingPongN defines a few days of ping pong hops after each deploy.
	defaultPingPongN = 100_000
	// defaultPingPongP defines 3 parallel ping pongs per edge.
	defaultPingPongP = 3
	// defaultPingPongL defines a single parallel ping pongs to use Latest confirmation level. This decreases on-chain costs.
	defaultPingPongL = 1
)

func DefaultDeployConfig() DeployConfig {
	return DeployConfig{
		PingPongN: defaultPingPongN,
		PingPongP: defaultPingPongP,
		PingPongL: defaultPingPongL,
	}
}

type DeployConfig struct {
	PingPongN uint64 // Number of hops per ping pong.
	PingPongP uint64 // Number of parallel ping pongs to start per edge.
	PingPongL uint64 // Number of parallel ping pongs to use Latest confirmation level.

	// Internal use parameters (no command line flags).
	testConfig bool
}

// Deploy a new e2e network. It also starts all services in order to deploy private portals.
// It also returns an optional deployed ping pong contract is enabled.
func Deploy(ctx context.Context, def Definition, cfg DeployConfig) (*pingpong.XDapp, error) {
	if def.Testnet.Network.IsProtected() {
		// If a protected network needs to be deployed temporarily comment out this check.
		return nil, errors.New("cannot deploy protected network", "network", def.Testnet.Network)
	}

	const genesisValSetID = 1 // validator set IDs start at 1

	genesisVals, err := toPortalValidators(def.Testnet.Validators)
	if err != nil {
		return nil, err
	}

	if err := Setup(ctx, def, cfg); err != nil {
		return nil, err
	}

	// Only stop and delete existing network right before actually starting new ones.
	if err := CleanInfra(ctx, def); err != nil {
		return nil, err
	}

	if err := StartInitial(ctx, def.Testnet.Testnet, def.Infra); err != nil {
		return nil, err
	}

	if err := waitForEVMs(ctx, def.Testnet.EVMChains(), def.Backends()); err != nil {
		return nil, err
	}
	logRPCs(ctx, def)

	contracts.UseStagingOmniRPC(def.Testnet.BroadcastOmniEVM().ExternalRPC)

	svmErr := svmInitAsync(ctx, def)

	if err = fundAnvil(ctx, def); err != nil {
		return nil, errors.Wrap(err, "fund anvil")
	}

	if err = deployAllCreate3(ctx, def); err != nil {
		return nil, errors.Wrap(err, "deploy create3")
	}

	// Deploy portals (allowing networkFromDef to succeed)
	if err := def.Netman().DeployPortals(ctx, genesisValSetID, genesisVals); err != nil {
		return nil, errors.Wrap(err, "deploy portals")
	}

	var eg1 errgroup.Group
	eg1.Go(func() error { return maybeDeploySolver(ctx, def) })                                      // Deploy solver before initPortalRegistry, so solver detects boxes after netconf.Await
	eg1.Go(func() error { return maybeDeployL1OmniERC20(ctx, networkFromDef(def), def.Backends()) }) // Solver also requires all tokens to be deployed
	if err := eg1.Wait(); err != nil {
		return nil, errors.Wrap(err, "deploy solver and erc20 contracts")
	}

	// Deploy other contracts (and other on-chain setup)
	var eg2 errgroup.Group
	eg2.Go(func() error { return initPortalRegistry(ctx, def) })
	eg2.Go(func() error { return allowStagingValidators(ctx, def) })
	eg2.Go(func() error { return maybeSubmitNetworkUpgrades(ctx, def) })
	eg2.Go(func() error { return FundValidatorsForTesting(ctx, def) })
	if err := eg2.Wait(); err != nil {
		return nil, errors.Wrap(err, "deploy other contracts")
	}

	err = waitForSupportedChains(ctx, def)
	if err != nil {
		return nil, err
	}

	if err := <-svmErr; err != nil {
		return nil, errors.Wrap(err, "svm init")
	}

	if cfg.PingPongN == 0 || def.Testnet.Network == netconf.Mainnet {
		return nil, nil //nolint:nilnil // No ping pong, no XDapp to return.
	}

	pp, err := pingpong.Deploy(ctx, networkFromDef(def), def.Backends()) // Safe to call networkFromDef since this after netman.DeployContracts
	if err != nil {
		return nil, errors.Wrap(err, "deploy pingpong")
	}

	err = pp.StartAllEdges(ctx, cfg.PingPongL, cfg.PingPongP, cfg.PingPongN)
	if err != nil {
		return nil, errors.Wrap(err, "start all edges")
	}

	return &pp, nil
}

// E2ETestConfig is the configuration required to run a full e2e test.
type E2ETestConfig struct {
	Preserve bool
}

// DefaultE2ETestConfig returns a default configuration for a e2e test.
func DefaultE2ETestConfig() E2ETestConfig {
	return E2ETestConfig{}
}

// E2ETest runs a full e2e test.
func E2ETest(ctx context.Context, def Definition, cfg E2ETestConfig) error {
	var pingpongN = uint64(3)
	const pingpongP = uint64(3)
	const pingpongL = uint64(2)
	if def.Manifest.PingPongN != 0 {
		pingpongN = def.Manifest.PingPongN
	}

	depCfg := DeployConfig{
		PingPongN:  pingpongN,
		PingPongP:  pingpongP,
		PingPongL:  pingpongL,
		testConfig: true,
	}

	pingpong, err := Deploy(ctx, def, depCfg)
	if err != nil {
		return err
	}

	stopReceiptMonitor := StartMonitoringReceipts(ctx, def)

	stopValidatorUpdates := StartValidatorUpdates(ctx, def)

	stopAddingPortals := startAddingMockPortals(ctx, def)

	msgBatches := []int{3, 2, 1} // Send 6 msgs from each chain to each other chain
	msgsErr := StartSendingXMsgs(ctx, def.Testnet.Network, def.Netman(), def.Backends(), msgBatches...)

	if err := StartRemaining(ctx, def.Testnet.Testnet, def.Infra); err != nil {
		return err
	}

	if err := Wait(ctx, def.Testnet.Testnet, 5); err != nil { // allow some txs to go through
		return err
	}

	// Stop before perturbations, since they cause portal txs to fail and e2e tests to flap.
	if err := stopAddingPortals(); err != nil {
		return errors.Wrap(err, "stop adding portals")
	}

	if def.Testnet.HasPerturbations() {
		if err := perturb(ctx, def.Testnet); err != nil {
			return err
		}
	}

	if def.Testnet.Evidence > 0 {
		valAddr, err := injectEvidence(ctx, def.Testnet)
		if err != nil {
			return errors.Wrap(err, "inject evidence")
		}

		if err := awaitSlashed(ctx, def, valAddr); err != nil {
			return errors.Wrap(err, "await slashed")
		}
	}

	// Start unit tests.
	if err := Test(ctx, def, TestConfig{}); err != nil {
		return err
	}

	// Wait for:
	// - all xmsgs messages to be sent
	// - all xmsgs to be submitted
	// - all pingpongs to complete
	// - all receipts are successful

	log.Info(ctx, "Waiting for all cross chain messages to be sent")
	select {
	case <-ctx.Done():
		return errors.Wrap(ctx.Err(), "cancel")
	case err := <-msgsErr:
		if err != nil {
			return err
		}
	}

	network := networkFromDef(def) // Safe to call networkFromDef since this after netman.DeployContracts
	if err := WaitAllSubmissions(ctx, network, def.Netman().Portals(), sum(msgBatches)); err != nil {
		return err
	}

	if err := pingpong.LogBalances(ctx); err != nil {
		return err
	}

	if err := pingpong.WaitDone(ctx); err != nil {
		return errors.Wrap(err, "wait for pingpong")
	}

	if err := stopReceiptMonitor(); err != nil {
		return errors.Wrap(err, "stop deploy")
	}

	if err := stopValidatorUpdates(); err != nil {
		return errors.Wrap(err, "stop validator updates")
	}

	if err := LogMetrics(ctx, def); err != nil {
		return err
	}

	if err := detectPanics(ctx, def); err != nil {
		return err
	}

	if cfg.Preserve {
		log.Warn(ctx, "Docker containers not stopped, --preserve=true", nil)
	} else if err := CleanInfra(ctx, def); err != nil {
		return err
	}

	return nil
}

// Upgrade generates all local artifacts, but only copies the dynamic artifacts (excl genesis) to the VMs.
// It then calls docker-compose up.
func Upgrade(ctx context.Context, def Definition, cfg DeployConfig, upgradeCfg types.ServiceConfig) error {
	if err := Setup(ctx, def, cfg); err != nil {
		return err
	}

	return def.Infra.Upgrade(ctx, upgradeCfg)
}

// Restart calls docker-compose down-up on all VMs.
func Restart(ctx context.Context, def Definition, cfg DeployConfig, upgradeCfg types.ServiceConfig) error {
	if err := Setup(ctx, def, cfg); err != nil {
		return err
	}

	return def.Infra.Restart(ctx, upgradeCfg)
}

// toPortalValidators returns the provided validator set as a lice of portal validators.
func toPortalValidators(validators map[*e2e.Node]int64) ([]bindings.Validator, error) {
	vals := make([]bindings.Validator, 0, len(validators))
	for val, power := range validators {
		addr, err := k1util.PubKeyToAddress(val.PrivvalKey.PubKey())
		if err != nil {
			return nil, errors.Wrap(err, "convert validator pubkey to address")
		}

		power, err := umath.ToUint64(power)
		if err != nil {
			return nil, err
		}

		vals = append(vals, bindings.Validator{
			Addr:  addr,
			Power: power,
		})
	}

	return vals, nil
}

func logRPCs(ctx context.Context, def Definition) {
	endpoints := ExternalEndpoints(def)
	for _, chain := range def.Testnet.EVMChains() {
		rpc, _ := endpoints.ByNameOrID(chain.Name, chain.ChainID)
		log.Info(ctx, "EVM Chain RPC available", "chain_id", chain.ChainID,
			"chain_name", chain.Name, "url", rpc)
	}
}

// waitForSupportedChains waits for all dest chains to be supported by all src chains.
func waitForSupportedChains(ctx context.Context, def Definition) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	attempt := 1

	for {
		select {
		case <-ctx.Done():
			return errors.Wrap(ctx.Err(), "cancel")
		case <-ticker.C:
			ok, err := checkSupportedChains(ctx, def.Netman())
			if err != nil {
				return err
			} else if ok {
				return nil
			}

			if attempt > 60 {
				return errors.New("timeout waiting for supported chains")
			} else if attempt%10 == 0 {
				log.Debug(ctx, "Waiting for supported chains", "attempt", attempt)
			}
			attempt++
		}
	}
}

func checkSupportedChains(ctx context.Context, n netman.Manager) (bool, error) {
	for _, src := range n.Portals() {
		for _, dest := range n.Portals() {
			if src.Chain.ChainID == dest.Chain.ChainID {
				continue
			}

			supported, err := src.Contract.IsSupportedDest(&bind.CallOpts{Context: ctx}, dest.Chain.ChainID)
			if err != nil {
				return false, errors.Wrap(err, "check supported chain")
			} else if !supported {
				return false, nil
			}
		}
	}

	return true, nil
}

// maybeSubmitNetworkUpgrades submits multiple network upgrade up to latest (if required).
func maybeSubmitNetworkUpgrades(ctx context.Context, def Definition) error {
	network := def.Testnet.Network

	client, err := def.Testnet.BroadcastNode().Client()
	if err != nil {
		return errors.Wrap(err, "broadcast client")
	}
	cprov := provider.NewABCI(client, network)

	backend, err := def.Backends().Backend(network.Static().OmniExecutionChainID)
	if err != nil {
		return err
	}

	contract, err := bindings.NewUpgrade(common.HexToAddress(predeploys.Upgrade), backend)
	if err != nil {
		return err
	}

	txOpts, err := backend.BindOpts(ctx, eoa.MustAddress(network, eoa.RoleUpgrader))
	if err != nil {
		return err
	}

	// Genesis always includes the first upgrade after ephemeral genesis at block=1.
	upgrade, ok, err := static.NextUpgrade(def.Manifest.EphemeralGenesis)
	if err != nil {
		return err
	} else if !ok {
		// No genesis upgrade needed, ephemeral genesis is latest upgrade.
		if static.LatestUpgrade() != def.Manifest.EphemeralGenesis {
			return errors.New("ephemeral genesis is not the latest upgrade", "ephemeral", def.Manifest.EphemeralGenesis, "latest", static.LatestUpgrade())
		}

		return nil
	}

	for i := 0; ; i++ {
		// Get the next upgrade to submit
		upgrade, ok, err = static.NextUpgrade(upgrade)
		if err != nil {
			return err
		} else if !ok {
			return nil // No next upgrade to plan
		}

		height, err := backend.BlockNumber(ctx)
		if err != nil {
			return err
		}

		// Upgrades fail if processed after height (mempool is non-deterministic, so we need a buffer).
		const delayDevnet uint64 = 5
		const delayStaging uint64 = 30 // Staging txs are signed by fireblocks, which is slow
		delay := delayDevnet
		if def.Testnet.Network == netconf.Staging {
			delay = delayStaging
		}
		height += delay

		log.Info(ctx, "Planning upgrade", "height", height, "name", upgrade, "delay", delay)

		tx, err := contract.PlanUpgrade(txOpts, bindings.UpgradePlan{
			Name:   upgrade,
			Height: height,
			Info:   "e2e triggered upgrade",
		})
		if err != nil {
			return errors.Wrap(err, "plan upgrade")
		}
		if _, err := backend.WaitMined(ctx, tx); err != nil {
			return errors.Wrap(err, "wait mined")
		}

		if _, ok, err := static.NextUpgrade(upgrade); err != nil {
			return err
		} else if !ok {
			return nil // No next upgrade to plan, just return
		}

		waitHeight, err := umath.ToInt64(height + 1)
		if err != nil {
			return err
		}

		// Wait for upgrade height to be processed.
		if _, _, err := waitForHeight(ctx, def.Testnet.Testnet, waitHeight); err != nil {
			return errors.Wrap(err, "wait for height")
		}

		if current, err := queryutil.CurrentUpgrade(ctx, cprov); err != nil {
			return errors.Wrap(err, "current upgrade")
		} else if current != upgrade {
			return errors.New("upgrade not applied", "current", current, "want", upgrade)
		}

		log.Info(ctx, "Upgrade applied", "height", height, "name", upgrade) // We don't actually confirm this...
	}
}
