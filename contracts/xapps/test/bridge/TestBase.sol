// SPDX-License-Identifier: GPL-3.0-only
pragma solidity 0.8.26;

import { StablecoinUpgradeable } from "rlusd/contracts/StablecoinUpgradeable.sol";
import { StablecoinProxy } from "rlusd/contracts/StablecoinProxy.sol";

import { Lockbox, ILockbox } from "src/bridge/Lockbox.sol";
import { Bridge, IBridge } from "src/bridge/Bridge.sol";
import { TransparentUpgradeableProxy } from "@openzeppelin/contracts/proxy/transparent/TransparentUpgradeableProxy.sol";

import { PausableUpgradeable } from "@openzeppelin/contracts-upgradeable/utils/PausableUpgradeable.sol";
import { IAccessControl } from "@openzeppelin/contracts-upgradeable/access/AccessControlUpgradeable.sol";

import { Test } from "forge-std/Test.sol";
import { SafeTransferLib } from "solady/src/utils/SafeTransferLib.sol";
import { MockPortal } from "core/test/utils/MockPortal.sol";

contract TestBase is Test {
    StablecoinUpgradeable internal token;
    StablecoinUpgradeable internal wrapper;

    Lockbox internal lockbox;
    Bridge internal bridgeWithLockbox;
    Bridge internal bridgeNoLockbox;

    MockPortal internal omni;

    uint64 internal constant SRC_CHAIN_ID = 1;
    uint64 internal constant DEST_CHAIN_ID = 2;
    uint64 internal constant DEFAULT_RECEIVE_GAS_LIMIT = 150_000;
    uint64 internal constant DEFAULT_RECEIVE_LOCKBOX_GAS_LIMIT = 250_000;
    uint256 internal constant INITIAL_USER_BALANCE = 1_000_000 ether;

    address internal user = makeAddr("user");
    address internal other = makeAddr("other");
    address internal admin = makeAddr("admin");
    address internal minter = makeAddr("minter");
    address internal pauser = makeAddr("pauser");
    address internal unpauser = makeAddr("unpauser");
    address internal upgrader = makeAddr("upgrader");
    address internal clawbacker = makeAddr("clawbacker");
    address internal configurer = makeAddr("configurer");
    address internal authorizer = makeAddr("authorizer");

    modifier prank(address addr) {
        vm.startPrank(addr);
        _;
        vm.stopPrank();
    }

    function setUp() public virtual {
        deploy();
        configure();
        vm.chainId(SRC_CHAIN_ID);
    }

    function deploy() internal {
        omni = new MockPortal();
        _deployTokens();
        _deployInfra();
    }

    function configure() internal {
        _fundAddr(user);
        _configureApprovals();
        _configureRoutes();
        _configurePermissions();
    }

    function mockBridgeSend(
        Bridge bridge,
        uint64 srcChainId,
        uint64 destChainId,
        bool wrap,
        address refundTo,
        address from,
        address to,
        uint256 value
    ) internal {
        uint256 fee = bridge.bridgeFee(destChainId);

        vm.chainId(srcChainId);
        vm.prank(from);
        vm.expectEmit(true, true, true, true);
        emit IBridge.TokenSent(destChainId, from, to, value);
        bridge.sendToken{ value: fee }(destChainId, to, value, wrap, refundTo);
    }

    function mockBridgeReceive(Bridge bridge, uint64 srcChainId, uint64 destChainId, address to, uint256 value)
        internal
    {
        (address destination,) = bridge.getRoute(destChainId);
        bytes memory data = abi.encodeCall(Bridge.receiveToken, (to, value));

        vm.chainId(destChainId);
        vm.expectEmit(true, true, true, true);
        emit IBridge.TokenReceived(srcChainId, to, value, true);
        omni.mockXCall({
            sourceChainId: srcChainId,
            sender: address(bridge),
            to: destination,
            data: data,
            gasLimit: _getGasLimit(Bridge(destination))
        });
    }

    function mockBridge(
        Bridge bridge,
        uint64 srcChainId,
        uint64 destChainId,
        bool wrap,
        address refundTo,
        address from,
        address to,
        uint256 value
    ) internal {
        mockBridgeSend(bridge, srcChainId, destChainId, wrap, refundTo, from, to, value);
        mockBridgeReceive(bridge, srcChainId, destChainId, to, value);

        vm.chainId(srcChainId);
    }

    function _deployTokens() internal {
        token = _deployToken("Ripple USD", "RLUSD");
        wrapper = _deployToken("Bridged RLUSD (Omni)", "RLUSD.e");
    }

    function _deployInfra() internal {
        lockbox = _deployLockbox(address(token), address(wrapper));
        bridgeWithLockbox = _deployBridge(address(wrapper), address(lockbox));
        bridgeNoLockbox = _deployBridge(address(wrapper), address(0));
    }

    function _deployToken(string memory name, string memory symbol) internal returns (StablecoinUpgradeable) {
        address impl = address(new StablecoinUpgradeable());
        bytes memory data = abi.encodeCall(
            StablecoinUpgradeable.initialize, (name, symbol, minter, admin, upgrader, pauser, clawbacker)
        );

        address proxy = address(new StablecoinProxy(impl, data));
        return StablecoinUpgradeable(proxy);
    }

    function _deployLockbox(address token_, address wrapper_) internal returns (Lockbox) {
        address impl = address(new Lockbox());
        bytes memory data = abi.encodeCall(Lockbox.initialize, (admin, pauser, unpauser, token_, wrapper_));

        address proxy = address(new TransparentUpgradeableProxy(impl, admin, data));
        return Lockbox(proxy);
    }

    function _deployBridge(address token_, address lockbox_) internal returns (Bridge) {
        address impl = address(new Bridge(DEFAULT_RECEIVE_GAS_LIMIT, DEFAULT_RECEIVE_LOCKBOX_GAS_LIMIT));
        bytes memory data = abi.encodeCall(
            Bridge.initialize, (admin, configurer, authorizer, pauser, unpauser, address(omni), token_, lockbox_)
        );

        address proxy = address(new TransparentUpgradeableProxy(impl, admin, data));
        return Bridge(proxy);
    }

    function _fundAddr(address addr) internal {
        vm.deal(addr, 1 ether);
        vm.prank(minter);
        token.mint(addr, INITIAL_USER_BALANCE);
    }

    function _getGasLimit(Bridge bridge) internal view returns (uint64) {
        return bridge.lockbox() == address(0) ? DEFAULT_RECEIVE_GAS_LIMIT : DEFAULT_RECEIVE_LOCKBOX_GAS_LIMIT;
    }

    function _configureApprovals() internal {
        vm.startPrank(user);

        // Approve source lockbox to wrap original tokens.
        token.approve(address(lockbox), type(uint256).max);

        // Approve source bridge to transfer original tokens.
        token.approve(address(bridgeWithLockbox), type(uint256).max);

        // Approve both bridge types to transfer wrapped tokens.
        wrapper.approve(address(bridgeWithLockbox), type(uint256).max);
        wrapper.approve(address(bridgeNoLockbox), type(uint256).max);

        vm.stopPrank();
    }

    function _configureRoutes() internal {
        uint64[] memory chainIds = new uint64[](1);
        IBridge.Route[] memory routes = new IBridge.Route[](1);

        chainIds[0] = DEST_CHAIN_ID;
        routes[0] = IBridge.Route({ bridge: address(bridgeNoLockbox), hasLockbox: false });
        vm.prank(configurer);
        bridgeWithLockbox.configureRoutes(chainIds, routes);
        vm.prank(authorizer);
        bridgeWithLockbox.authorizeRoutes(chainIds, routes);

        chainIds[0] = SRC_CHAIN_ID;
        routes[0] = IBridge.Route({ bridge: address(bridgeWithLockbox), hasLockbox: true });
        vm.prank(configurer);
        bridgeNoLockbox.configureRoutes(chainIds, routes);
        vm.prank(authorizer);
        bridgeNoLockbox.authorizeRoutes(chainIds, routes);
    }

    function _configurePermissions() internal {
        vm.startPrank(admin);

        wrapper.grantRole(wrapper.MINTER_ROLE(), address(lockbox));
        wrapper.grantRole(wrapper.CLAWBACKER_ROLE(), address(lockbox));

        wrapper.grantRole(wrapper.MINTER_ROLE(), address(bridgeWithLockbox));
        wrapper.grantRole(wrapper.CLAWBACKER_ROLE(), address(bridgeWithLockbox));

        wrapper.grantRole(wrapper.MINTER_ROLE(), address(bridgeNoLockbox));
        wrapper.grantRole(wrapper.CLAWBACKER_ROLE(), address(bridgeNoLockbox));

        vm.stopPrank();
    }

    function _assertBalances(address addr, uint256 tokenUserBal, uint256 tokenLockboxBal, uint256 wrapperUserBal)
        internal
        view
    {
        assertEq(token.balanceOf(addr), tokenUserBal, "INIT: Token balance mismatch");
        assertEq(token.balanceOf(address(lockbox)), tokenLockboxBal, "INIT: Lockbox balance mismatch");
        assertEq(wrapper.balanceOf(addr), wrapperUserBal, "INIT: Wrapper balance mismatch");
    }
}
