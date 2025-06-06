package anvil

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"text/template"
	"time"

	"github.com/omni-network/omni/e2e/app/eoa"
	"github.com/omni-network/omni/lib/bi"
	"github.com/omni-network/omni/lib/errors"
	"github.com/omni-network/omni/lib/ethclient"
	"github.com/omni-network/omni/lib/log"
	"github.com/omni-network/omni/scripts"

	_ "embed"
)

type Option func(*options)

type options struct {
	flags []string
}

func WithFork(forkURL string) Option {
	return func(o *options) {
		o.flags = append(o.flags, "--fork-url="+forkURL)
	}
}

func WithAutoImpersonate() Option {
	return func(o *options) {
		o.flags = append(o.flags, "--auto-impersonate")
	}
}

func WithBlockTime(seconds float64) Option {
	return func(o *options) {
		o.flags = append(o.flags, fmt.Sprintf("--block-time=%f", seconds))
	}
}

func WithSlotsInEpoch(slots uint64) Option {
	return func(o *options) {
		o.flags = append(o.flags, fmt.Sprintf("--slots-in-an-epoch=%d", slots))
	}
}

func WithFlags(flags ...string) Option {
	return func(o *options) {
		o.flags = append(o.flags, flags...)
	}
}

// Start starts an anvil node and returns an ethclient and a stop function or an error.
func Start(ctx context.Context, dir string, chainID uint64, opts ...Option) (ethclient.Client, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute) // Allow 1 minute for edge case of pulling images.
	defer cancel()
	if !composeDown(ctx, dir) {
		return nil, nil, errors.New("failure to clean up previous anvil instance")
	}

	o := options{}
	for _, opt := range opts {
		opt(&o)
	}

	// Ensure ports are available
	port, err := getAvailablePort()
	if err != nil {
		return nil, nil, errors.Wrap(err, "get available port")
	}

	if err := writeComposeFile(dir, chainID, port, scripts.FoundryVersion(), o.flags); err != nil {
		return nil, nil, errors.Wrap(err, "write compose file")
	}

	log.Info(ctx, "Starting anvil")

	out, err := execCmd(ctx, dir, "docker", "compose", "up", "-d", "--remove-orphans")
	if err != nil {
		return nil, nil, errors.Wrap(err, "docker compose up: "+out)
	}

	endpoint := "http://localhost:" + port

	ethCl, err := ethclient.DialContext(ctx, "anvil", endpoint)
	if err != nil {
		return nil, nil, errors.Wrap(err, "new eth client")
	}

	stop := func() { //nolint:contextcheck // Fresh context required for stopping.
		// Fresh stop context since above context might be canceled.
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()
		composeDown(stopCtx, dir)
	}

	// Wait for RPC to be available
	// If forking, wait longer since it needs to sync state
	retryCount := 10 // 10 seconds for non-fork
	retryInterval := time.Second
	if isFork(o) {
		retryCount = 60 // 60 seconds for fork
		retryInterval = time.Second * 5
	}

	for i := 0; i < retryCount; i++ {
		if i == retryCount-1 {
			stop()
			return nil, nil, errors.New("wait for RPC timed out")
		}

		select {
		case <-ctx.Done():
			return nil, nil, errors.Wrap(ctx.Err(), "timeout")
		case <-time.After(retryInterval):
		}

		_, err := ethCl.BlockNumber(ctx)
		if err == nil {
			break
		}

		log.Warn(ctx, "Anvil: waiting for RPC to be available", err)
	}

	// always fund dev accounts
	eth1m := bi.Ether(1_000_000) // 1M ETH
	if err := FundAccounts(ctx, ethCl, eth1m, eoa.DevAccounts()...); err != nil {
		stop()
		return nil, nil, errors.Wrap(err, "fund accounts")
	}

	log.Info(ctx, "Anvil: RPC is available", "addr", endpoint)

	return ethCl, stop, nil
}

// isFork returns true if the --fork-url flag is set.
func isFork(opts options) bool {
	for _, flag := range opts.flags {
		if strings.HasPrefix(flag, "--fork-url") {
			return true
		}
	}

	return false
}

// composeDown runs docker-compose down in the provided directory.
func composeDown(ctx context.Context, dir string) bool {
	if _, err := os.Stat(dir + "/compose.yaml"); os.IsNotExist(err) {
		return true
	}

	out, err := execCmd(ctx, dir, "docker", "compose", "down")
	if err != nil {
		log.Error(ctx, "Error: docker compose down", err, "out", out)
		return false
	}

	log.Debug(ctx, "Anvil: docker compose down: ok")

	return true
}

func execCmd(ctx context.Context, dir string, cmd string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, cmd, args...)
	c.Dir = dir

	out, err := c.CombinedOutput()
	if err != nil {
		return string(out), errors.Wrap(err, "exec", "out", string(out))
	}

	return string(out), nil
}

//go:embed compose.yaml.tmpl
var composeTpl []byte

func writeComposeFile(dir string, chainID uint64, port, foundryVer string, flags []string) error {
	tpl, err := template.New("").Parse(string(composeTpl))
	if err != nil {
		return errors.Wrap(err, "parse compose template")
	}

	var buf bytes.Buffer
	err = tpl.Execute(&buf, struct {
		ChainID uint64
		Port    string
		Version string
		Flags   []string
	}{
		ChainID: chainID,
		Port:    port,
		Version: foundryVer,
		Flags:   flags,
	})
	if err != nil {
		return errors.Wrap(err, "execute compose template")
	}

	err = os.WriteFile(dir+"/compose.yaml", buf.Bytes(), 0644)
	if err != nil {
		return errors.Wrap(err, "write compose file")
	}

	return nil
}

func getAvailablePort() (string, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return "", errors.Wrap(err, "resolve addr")
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return "", errors.Wrap(err, "listen")
	}
	defer l.Close()

	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		return "", errors.Wrap(err, "split host port")
	}

	return port, nil
}
