package keeper

import (
	"bytes"
	"context"
	"testing"
	"time"

	attesttypes "github.com/omni-network/omni/halo/attest/types"
	"github.com/omni-network/omni/lib/bi"
	"github.com/omni-network/omni/lib/errors"
	"github.com/omni-network/omni/lib/ethclient"
	"github.com/omni-network/omni/lib/k1util"
	"github.com/omni-network/omni/lib/tutil"
	etypes "github.com/omni-network/omni/octane/evmengine/types"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	cmttime "github.com/cometbft/cometbft/types/time"

	"github.com/ethereum/go-ethereum"
	eengine "github.com/ethereum/go-ethereum/beacon/engine"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/trie"

	"cosmossdk.io/core/store"
	storetypes "cosmossdk.io/store/types"
	"cosmossdk.io/x/tx/signing"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	cosmosstd "github.com/cosmos/cosmos-sdk/std"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authcodec "github.com/cosmos/cosmos-sdk/x/auth/codec"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	atypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	btypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	dtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	stypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/gogoproto/proto"
	fuzz "github.com/google/gofuzz"
	"github.com/stretchr/testify/require"
)

var zeroAddr common.Address
var zeroHash common.Hash

const maxWithdrawalsPerBlock uint64 = 32

func TestKeeper_PrepareProposal(t *testing.T) {
	t.Parallel()

	// TestRunErrScenarios tests various error scenarios in the PrepareProposal function.
	// It covers cases where different errors are encountered during the preparation of a proposal,
	// such as when no transactions are provided, when errors occur while fetching block information,
	// or when errors occur during fork choice update.
	t.Run("TestRunErrScenarios", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name       string
			mockEngine mockEngineAPI
			req        *abci.RequestPrepareProposal
			wantErr    bool
		}{
			{
				name:       "no transactions",
				mockEngine: mockEngineAPI{},
				req: &abci.RequestPrepareProposal{
					Txs:    nil,        // Set to nil to simulate no transactions
					Height: 1,          // Set height to 1 for this test case
					Time:   time.Now(), // Set time to current time or mock a time
				},
				wantErr: false,
			},
			{
				name:       "with  transactions",
				mockEngine: mockEngineAPI{},
				req: &abci.RequestPrepareProposal{
					Txs:    [][]byte{[]byte("tx1")}, // simulate transactions
					Height: 1,
					Time:   time.Now(),
				},
				wantErr: true,
			},
			{
				name: "forkchoiceUpdateV3  not valid",
				mockEngine: mockEngineAPI{
					headerByTypeFunc: func(context.Context, ethclient.HeadType) (*types.Header, error) {
						fuzzer := ethclient.NewFuzzer(0)
						var header *types.Header
						fuzzer.Fuzz(&header)

						return header, nil
					},
					forkchoiceUpdatedV3Func: func(ctx context.Context, update eengine.ForkchoiceStateV1,
						payloadAttributes *eengine.PayloadAttributes) (eengine.ForkChoiceResponse, error) {
						return eengine.ForkChoiceResponse{
							PayloadStatus: eengine.PayloadStatusV1{
								Status:          eengine.INVALID,
								LatestValidHash: nil,
								ValidationError: nil,
							},
							PayloadID: nil,
						}, nil
					},
				},
				req: &abci.RequestPrepareProposal{
					Txs:    nil,
					Height: 2,
					Time:   time.Now(),
				},
				wantErr: true,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				ctx, storeService := setupCtxStore(t, nil)
				cdc := getCodec(t)
				txConfig := authtx.NewTxConfig(cdc, nil)

				var err error
				tt.mockEngine.EngineClient, err = ethclient.NewEngineMock()
				require.NoError(t, err)

				ap := mockAddressProvider{
					address: tutil.RandomAddress(),
				}
				frp := newRandomFeeRecipientProvider()
				k, err := NewKeeper(cdc, storeService, &tt.mockEngine, txConfig, ap, frp, maxWithdrawalsPerBlock)
				require.NoError(t, err)
				populateGenesisHead(ctx, t, k)

				tt.req.MaxTxBytes = cmttypes.MaxBlockSizeBytes

				_, err = k.PrepareProposal(withRandomErrs(t, ctx), tt.req)
				if (err != nil) != tt.wantErr {
					t.Errorf("PrepareProposal() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
			})
		}
	})

	t.Run("TestBuildOptimistic", func(t *testing.T) {
		t.Parallel()
		// setup dependencies
		ctx, storeService := setupCtxStore(t, nil)
		cdc := getCodec(t)
		txConfig := authtx.NewTxConfig(cdc, nil)
		mockEngine, err := newMockEngineAPI(0)
		require.NoError(t, err)

		ap := mockAddressProvider{
			address: common.BytesToAddress([]byte("test")),
		}
		frp := newRandomFeeRecipientProvider()
		keeper, err := NewKeeper(cdc, storeService, &mockEngine, txConfig, ap, frp, maxWithdrawalsPerBlock, mockEventProc{})
		require.NoError(t, err)
		keeper.SetVoteProvider(mockVEProvider{})
		populateGenesisHead(ctx, t, keeper)

		// get the genesis block to build on top of
		ts := time.Now()
		latestBlock, err := mockEngine.HeaderByType(ctx, ethclient.HeadLatest)
		require.NoError(t, err)
		latestHeight := latestBlock.Number.Uint64()

		appHash1 := tutil.RandomHash()
		appHash2 := tutil.RandomHash()

		// build next two blocks and get the PayloadID of the second
		mockEngine.pushPayload(t, ctx, frp.LocalFeeRecipient(), latestBlock.Hash(), ts, appHash1)

		nextBlock, blockPayload := mockEngine.nextBlock(t, latestHeight+1, uint64(ts.Unix()), latestBlock.Hash(), ap.LocalAddress(), &appHash2)
		_, err = mockEngine.mock.NewPayloadV3(ctx, blockPayload, nil, &appHash2)
		require.NoError(t, err)
		payloadID := mockEngine.pushPayload(t, ctx, frp.LocalFeeRecipient(), nextBlock.Hash(), ts, appHash2)

		req := &abci.RequestPrepareProposal{
			Txs:        nil,
			Height:     int64(2),
			Time:       time.Now(),
			MaxTxBytes: cmttypes.MaxBlockSizeBytes,
		}

		// initialize mutable payload so we trigger the optimistic flow
		keeper.mutablePayload.Height = 2
		keeper.mutablePayload.UpdatedAt = time.Now()
		keeper.mutablePayload.ID = *payloadID

		resp, err := keeper.PrepareProposal(withRandomErrs(t, ctx), req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// decode the txn and get the messages
		tx, err := txConfig.TxDecoder()(resp.Txs[0])
		require.NoError(t, err)

		for _, msg := range tx.GetMsgs() {
			if _, ok := msg.(*etypes.MsgExecutionPayload); ok {
				assertExecutablePayload(t, msg, ts.Unix(), nextBlock.Hash(), frp, uint64(req.Height), 0)
			}
		}
	})

	t.Run("TestBuildNonOptimistic", func(t *testing.T) {
		t.Parallel()
		// setup dependencies
		ctx, storeService := setupCtxStore(t, nil)
		cdc := getCodec(t)
		txConfig := authtx.NewTxConfig(cdc, nil)

		mockEngine, err := newMockEngineAPI(0)
		require.NoError(t, err)

		ap := mockAddressProvider{
			address: common.BytesToAddress([]byte("test")),
		}
		frp := newRandomFeeRecipientProvider()
		keeper, err := NewKeeper(cdc, storeService, &mockEngine, txConfig, ap, frp, maxWithdrawalsPerBlock, mockEventProc{})
		require.NoError(t, err)
		keeper.SetVoteProvider(mockVEProvider{})
		populateGenesisHead(ctx, t, keeper)

		// Get the parent block we will build on top of
		head, err := keeper.getExecutionHead(ctx)
		require.NoError(t, err)
		headHash, err := head.Hash()
		require.NoError(t, err)

		req := &abci.RequestPrepareProposal{
			Txs:        nil,
			Height:     int64(2),
			Time:       time.Now(),
			MaxTxBytes: cmttypes.MaxBlockSizeBytes,
		}

		resp, err := keeper.PrepareProposal(withRandomErrs(t, ctx), req)
		tutil.RequireNoError(t, err)
		require.NotNil(t, resp)

		// decode the txn and get the messages
		tx, err := txConfig.TxDecoder()(resp.Txs[0])
		require.NoError(t, err)

		var actualDelCount int
		// assert that the message is an executable payload
		for _, msg := range tx.GetMsgs() {
			if _, ok := msg.(*etypes.MsgExecutionPayload); ok {
				assertExecutablePayload(t, msg, req.Time.Unix(), headHash, frp, head.GetBlockHeight()+1, 0)
			}
			if msgDelegate, ok := msg.(*stypes.MsgDelegate); ok {
				require.Equal(t, msgDelegate.Amount, sdk.NewInt64Coin("stake", 100))
				actualDelCount++
			}
		}
		// make sure all msg.Delegate are present
		require.Equal(t, 1, actualDelCount)
	})

	t.Run("TestBlobCommitments", func(t *testing.T) {
		t.Parallel()
		// setup dependencies
		ctx, storeService := setupCtxStore(t, nil)
		cdc := getCodec(t)
		txConfig := authtx.NewTxConfig(cdc, nil)

		commitment1 := tutil.RandomBytes(48)
		commitment2 := tutil.RandomBytes(48)

		mockEngine, err := newMockEngineAPI(0)
		require.NoError(t, err)
		mockEngine.getPayloadV3Func = func(ctx context.Context, payloadID eengine.PayloadID) (*eengine.ExecutionPayloadEnvelope, error) {
			resp, err := mockEngine.mock.GetPayloadV3(ctx, payloadID)
			if err != nil {
				return nil, err
			}

			resp.BlobsBundle.Commitments = []hexutil.Bytes{
				commitment1,
				commitment2,
			}

			return resp, nil
		}

		ap := mockAddressProvider{
			address: common.BytesToAddress([]byte("test")),
		}
		frp := newRandomFeeRecipientProvider()
		keeper, err := NewKeeper(cdc, storeService, &mockEngine, txConfig, ap, frp, maxWithdrawalsPerBlock, mockEventProc{})
		require.NoError(t, err)
		keeper.SetVoteProvider(mockVEProvider{})
		populateGenesisHead(ctx, t, keeper)

		// Get the parent block we will build on top of
		head, err := keeper.getExecutionHead(ctx)
		require.NoError(t, err)
		headHash, err := head.Hash()
		require.NoError(t, err)

		req := &abci.RequestPrepareProposal{
			Txs:        nil,
			Height:     int64(2),
			Time:       time.Now(),
			MaxTxBytes: cmttypes.MaxBlockSizeBytes,
		}

		resp, err := keeper.PrepareProposal(withRandomErrs(t, ctx), req)
		tutil.RequireNoError(t, err)
		require.NotNil(t, resp)

		// decode the txn and get the messages
		tx, err := txConfig.TxDecoder()(resp.Txs[0])
		require.NoError(t, err)

		// assert that the message is an executable payload
		var found bool
		for _, msg := range tx.GetMsgs() {
			payload, ok := msg.(*etypes.MsgExecutionPayload)
			if !ok {
				continue
			}

			found = true
			expected := [][]byte{commitment1, commitment2}
			require.Equal(t, expected, payload.BlobCommitments)
			assertExecutablePayload(t, msg, req.Time.Unix(), headHash, frp, head.GetBlockHeight()+1, len(expected))
		}
		require.True(t, found)
	})

	t.Run("TestSimpleEvmEvents", func(t *testing.T) {
		t.Parallel()
		// setup dependencies
		ctx, storeService := setupCtxStore(t, nil)

		cdc := getCodec(t)
		txConfig := authtx.NewTxConfig(cdc, nil)

		mockEngine, err := newMockEngineAPI(0)
		require.NoError(t, err)

		ap := mockAddressProvider{
			address: common.BytesToAddress([]byte("test")),
		}
		frp := newRandomFeeRecipientProvider()
		keeper, err := NewKeeper(cdc, storeService, &mockEngine, txConfig, ap, frp, maxWithdrawalsPerBlock, mockEventProc{})
		require.NoError(t, err)
		keeper.SetVoteProvider(mockVEProvider{})
		populateGenesisHead(ctx, t, keeper)

		// Get the parent block we will build on top of
		head, err := keeper.getExecutionHead(ctx)
		require.NoError(t, err)
		headHash, err := head.Hash()
		require.NoError(t, err)

		req := &abci.RequestPrepareProposal{
			Txs:        nil,
			Height:     int64(2),
			Time:       time.Now(),
			MaxTxBytes: cmttypes.MaxBlockSizeBytes,
		}

		resp, err := keeper.PrepareProposal(withRandomErrs(t, ctx), req)
		tutil.RequireNoError(t, err)
		require.NotNil(t, resp)

		// decode the txn and get the messages
		tx, err := txConfig.TxDecoder()(resp.Txs[0])
		require.NoError(t, err)

		for _, msg := range tx.GetMsgs() {
			if _, ok := msg.(*etypes.MsgExecutionPayload); ok {
				assertExecutablePayload(t, msg, req.Time.Unix(), headHash, frp, head.GetBlockHeight()+1, 0)
			}
		}
	})
}

func TestOptimistic(t *testing.T) {
	t.Parallel()

	const height int64 = 99

	// setup dependencies
	ctx, storeService := setupCtxStore(t, nil)
	cdc := getCodec(t)
	txConfig := authtx.NewTxConfig(cdc, nil)
	cmtAPI := newMockCometAPI(t, nil)
	mockEngine, err := newMockEngineAPI(0)
	require.NoError(t, err)

	vals, err := cmtAPI.Validators(ctx, height)
	require.NoError(t, err)

	// Optimistic build will trigger if we are next proposer
	nextProposer := vals.CopyIncrementProposerPriority(1).Proposer
	localAddr, err := k1util.PubKeyToAddress(nextProposer.PubKey)
	require.NoError(t, err)
	ap := mockAddressProvider{
		address: localAddr,
	}

	frp := newRandomFeeRecipientProvider()
	keeper, err := NewKeeper(cdc, storeService, &mockEngine, txConfig, ap, frp, maxWithdrawalsPerBlock, mockEventProc{})
	require.NoError(t, err)
	keeper.SetVoteProvider(mockVEProvider{})
	keeper.SetCometAPI(cmtAPI)
	keeper.SetBuildOptimistic(true)
	populateGenesisHead(ctx, t, keeper)

	timestamp := time.Now()
	ctx = ctx.
		WithBlockHeight(height).
		WithBlockTime(timestamp)

	err = keeper.PostFinalize(ctx)
	require.NoError(t, err)

	payloadID, h, ts := keeper.getOptimisticPayload()
	require.EqualValues(t, height+1, h)
	require.NotEmpty(t, ts)

	b, err := mockEngine.HeaderByType(ctx, ethclient.HeadLatest)
	require.NoError(t, err)

	env, err := mockEngine.GetPayloadV3(ctx, payloadID)
	require.NoError(t, err)

	payload := env.ExecutionPayload
	require.EqualValues(t, 1, payload.Number)
	require.EqualValues(t, timestamp.Unix(), payload.Timestamp)
	require.Equal(t, b.Hash(), payload.ParentHash)
	require.Equal(t, frp.LocalFeeRecipient(), payload.FeeRecipient)
	require.Empty(t, payload.Withdrawals)
}

// assertExecutablePayload asserts that the given message is an executable payload with the expected values.
func assertExecutablePayload(
	t *testing.T,
	msg sdk.Msg,
	ts int64,
	blockHash common.Hash,
	frp etypes.FeeRecipientProvider,
	height uint64,
	blobs int,
) {
	t.Helper()
	executionPayload, ok := msg.(*etypes.MsgExecutionPayload)
	require.True(t, ok)
	require.NotNil(t, executionPayload)

	payload := executionPayload.ExecutionPayloadDeneb
	require.Equal(t, int64(payload.Timestamp), ts)
	require.EqualValues(t, payload.PrevRandao, blockHash)
	require.EqualValues(t, payload.FeeRecipient, frp.LocalFeeRecipient())
	require.Empty(t, payload.Withdrawals)
	require.Equal(t, payload.BlockNumber, height)

	require.Len(t, executionPayload.BlobCommitments, blobs)
}

func ctxWithAppHash(t *testing.T, appHash common.Hash) context.Context {
	t.Helper()
	ctx, _ := setupCtxStore(t, &cmtproto.Header{AppHash: appHash.Bytes()})

	return ctx
}

func setupCtxStore(t *testing.T, header *cmtproto.Header) (sdk.Context, store.KVStoreService) {
	t.Helper()
	key := storetypes.NewKVStoreKey("test")
	storeService := runtime.NewKVStoreService(key)
	testCtx := testutil.DefaultContextWithDB(t, key, storetypes.NewTransientStoreKey("transient_test"))
	if header == nil {
		header = &cmtproto.Header{
			Time:    cmttime.Now(),
			AppHash: tutil.RandomHash().Bytes(),
			Height:  1,
		}
	}
	ctx := testCtx.Ctx.WithBlockHeader(*header)

	return ctx, storeService
}

func getCodec(t *testing.T) codec.Codec {
	t.Helper()
	sdkConfig := sdk.GetConfig()
	reg, err := codectypes.NewInterfaceRegistryWithOptions(codectypes.InterfaceRegistryOptions{
		ProtoFiles: proto.HybridResolver,
		SigningOptions: signing.Options{
			AddressCodec:          authcodec.NewBech32Codec(sdkConfig.GetBech32AccountAddrPrefix()),
			ValidatorAddressCodec: authcodec.NewBech32Codec(sdkConfig.GetBech32ValidatorAddrPrefix()),
		},
	})
	require.NoError(t, err)

	cosmosstd.RegisterInterfaces(reg)
	atypes.RegisterInterfaces(reg)
	stypes.RegisterInterfaces(reg)
	btypes.RegisterInterfaces(reg)
	dtypes.RegisterInterfaces(reg)
	etypes.RegisterInterfaces(reg)
	attesttypes.RegisterInterfaces(reg)

	return codec.NewProtoCodec(reg)
}

var _ ethclient.EngineClient = (*mockEngineAPI)(nil)
var _ etypes.AddressProvider = (*mockAddressProvider)(nil)
var _ etypes.EvmEventProcessor = (*mockEventProc)(nil)
var _ etypes.VoteExtensionProvider = (*mockVEProvider)(nil)

type mockEngineAPI struct {
	ethclient.EngineClient
	syncings                <-chan struct{}
	fuzzer                  *fuzz.Fuzzer
	mock                    ethclient.EngineClient // avoid repeating the implementation but also allow for custom implementations of mocks
	headerByTypeFunc        func(context.Context, ethclient.HeadType) (*types.Header, error)
	forkchoiceUpdatedV3Func func(context.Context, eengine.ForkchoiceStateV1, *eengine.PayloadAttributes) (eengine.ForkChoiceResponse, error)
	newPayloadV3Func        func(context.Context, eengine.ExecutableData, []common.Hash, *common.Hash) (eengine.PayloadStatusV1, error)
	getPayloadV3Func        func(ctx context.Context, payloadID eengine.PayloadID) (*eengine.ExecutionPayloadEnvelope, error)
}

// newMockEngineAPI returns a new mock engine API with a fuzzer and a mock engine client.
func newMockEngineAPI(syncings int) (mockEngineAPI, error) {
	me, err := ethclient.NewEngineMock()
	if err != nil {
		return mockEngineAPI{}, err
	}

	syncs := make(chan struct{}, syncings)
	for i := 0; i < syncings; i++ {
		syncs <- struct{}{}
	}

	return mockEngineAPI{
		mock:     me,
		syncings: syncs,
		fuzzer:   ethclient.NewFuzzer(time.Now().Truncate(time.Hour * 24).Unix()),
	}, nil
}

type mockAddressProvider struct {
	address common.Address
}

type mockVEProvider struct{}

func (mockVEProvider) PrepareVotes(context.Context, abci.ExtendedCommitInfo, uint64) ([]sdk.Msg, error) {
	coin := sdk.NewInt64Coin("stake", 100)
	msg := stypes.NewMsgDelegate("addr", "addr", coin)

	return []sdk.Msg{msg}, nil
}

type mockEventProc struct {
	deliverErr error
}

func (mockEventProc) Name() string {
	return "mock"
}

func (mockEventProc) FilterParams() ([]common.Address, [][]common.Hash) {
	return []common.Address{zeroAddr}, [][]common.Hash{{zeroHash}}
}

func (m mockEventProc) Deliver(_ context.Context, _ common.Hash, log etypes.EVMEvent) error {
	if !bytes.Equal(log.Address, zeroAddr.Bytes()) {
		panic("unexpected evm log address")
	}

	if len(log.Topics) != 1 {
		panic("unexpected evm log topics")
	}

	if !bytes.Equal(log.Topics[0], zeroHash.Bytes()) {
		panic("unexpected evm log topic")
	}

	if len(log.Data) != 0 {
		panic("unexpected evm log data")
	}

	return m.deliverErr
}

func (m mockAddressProvider) LocalAddress() common.Address {
	return m.address
}

func (m mockEngineAPI) maybeSync() (eengine.PayloadStatusV1, bool) {
	select {
	case <-m.syncings:
		return eengine.PayloadStatusV1{
			Status: eengine.SYNCING,
		}, true
	default:
		return eengine.PayloadStatusV1{}, false
	}
}

func (mockEngineAPI) FilterLogs(_ context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	return []types.Log{{
		BlockHash: *q.BlockHash,
		Address:   q.Addresses[0],
		Topics:    q.Topics[0],
	}}, nil
}

func (m mockEngineAPI) HeaderByType(ctx context.Context, typ ethclient.HeadType) (*types.Header, error) {
	if m.headerByTypeFunc != nil {
		return m.headerByTypeFunc(ctx, typ)
	}

	return m.mock.HeaderByType(ctx, typ)
}

//nolint:nonamedreturns // Required for defer
func (m mockEngineAPI) NewPayloadV3(ctx context.Context, params eengine.ExecutableData, versionedHashes []common.Hash, beaconRoot *common.Hash) (resp eengine.PayloadStatusV1, err error) {
	if status, ok := m.maybeSync(); ok {
		defer func() {
			resp.Status = status.Status
		}()
	}

	if m.newPayloadV3Func != nil {
		return m.newPayloadV3Func(ctx, params, versionedHashes, beaconRoot)
	}

	return m.mock.NewPayloadV3(ctx, params, versionedHashes, beaconRoot)
}

//nolint:nonamedreturns // Required for defer
func (m mockEngineAPI) ForkchoiceUpdatedV3(ctx context.Context, update eengine.ForkchoiceStateV1, payloadAttributes *eengine.PayloadAttributes) (resp eengine.ForkChoiceResponse, err error) {
	if status, ok := m.maybeSync(); ok {
		defer func() {
			resp.PayloadStatus.Status = status.Status
		}()
	}

	if m.forkchoiceUpdatedV3Func != nil {
		return m.forkchoiceUpdatedV3Func(ctx, update, payloadAttributes)
	}

	return m.mock.ForkchoiceUpdatedV3(ctx, update, payloadAttributes)
}

func (m mockEngineAPI) GetPayloadV3(ctx context.Context, payloadID eengine.PayloadID) (*eengine.ExecutionPayloadEnvelope, error) {
	if m.getPayloadV3Func != nil {
		return m.getPayloadV3Func(ctx, payloadID)
	}

	return m.mock.GetPayloadV3(ctx, payloadID)
}

// pushPayload - invokes the ForkchoiceUpdatedV3 method on the mock engine and returns the payload ID.
func (m mockEngineAPI) pushPayload(t *testing.T, ctx context.Context, feeRecipient common.Address, blockHash common.Hash, ts time.Time, appHash common.Hash) *eengine.PayloadID {
	t.Helper()
	state := eengine.ForkchoiceStateV1{
		HeadBlockHash:      blockHash,
		SafeBlockHash:      blockHash,
		FinalizedBlockHash: blockHash,
	}

	payloadAttrs := eengine.PayloadAttributes{
		Timestamp:             uint64(ts.Unix()),
		Random:                blockHash,
		SuggestedFeeRecipient: feeRecipient,
		Withdrawals:           []*types.Withdrawal{},
		BeaconRoot:            &appHash,
	}

	resp, err := m.ForkchoiceUpdatedV3(ctx, state, &payloadAttrs)
	tutil.RequireNoError(t, err)

	return resp.PayloadID
}

// nextBlock creates a new block with the given height, timestamp, parentHash, and feeRecipient. It also returns the
// payload for the block. It's a utility function for testing.
func (m mockEngineAPI) nextBlock(
	t *testing.T,
	height uint64,
	timestamp uint64,
	parentHash common.Hash,
	feeRecipient common.Address,
	beaconRoot *common.Hash,
	withdrawals ...*types.Withdrawal,
) (*types.Block, eengine.ExecutableData) {
	t.Helper()
	var header types.Header
	m.fuzzer.Fuzz(&header)
	header.Number = bi.N(height)
	header.Time = timestamp
	header.ParentHash = parentHash
	header.Coinbase = feeRecipient
	header.MixDigest = parentHash
	header.ParentBeaconRoot = beaconRoot

	// Convert header to block
	block := types.NewBlock(
		&header,
		&types.Body{Withdrawals: withdrawals},
		nil,
		trie.NewStackTrie(nil),
	)

	// Convert block to payload
	env := eengine.BlockToExecutableData(block, bi.Zero(), nil, nil)
	payload := *env.ExecutionPayload

	// Ensure the block is valid
	_, err := eengine.ExecutableDataToBlock(payload, nil, beaconRoot, nil)
	require.NoError(t, err)

	return block, payload
}

func withRandomErrs(t *testing.T, ctx sdk.Context) sdk.Context {
	t.Helper()
	return ctx.WithContext(ethclient.WithRandomErr(ctx, t))
}

var _ etypes.FeeRecipientProvider = testFeeRecipientProvider{}

type testFeeRecipientProvider common.Address

func newRandomFeeRecipientProvider() testFeeRecipientProvider {
	return testFeeRecipientProvider(common.BytesToAddress(tutil.RandomBytes(20)))
}

func (t testFeeRecipientProvider) LocalFeeRecipient() common.Address {
	return common.Address(t)
}

func (t testFeeRecipientProvider) VerifyFeeRecipient(address common.Address) error {
	if address != common.Address(t) {
		return errors.New("fee recipient not the random test address", "addr", address.Hex())
	}

	return nil
}
