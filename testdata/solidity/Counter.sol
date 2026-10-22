// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

// Counter verifies basic Solidity deployment, persistent storage, msg.sender,
// event emission, and calls from multiple EVM accounts.
contract Counter {
    uint256 public value;
    address public lastCaller;

    event Incremented(address indexed caller, uint256 delta, uint256 value);
    event Reset(address indexed caller);

    function increment() external returns (uint256) {
        return incrementBy(1);
    }

    function incrementBy(uint256 delta) public returns (uint256) {
        require(delta > 0, "delta is zero");
        value += delta;
        lastCaller = msg.sender;
        emit Incremented(msg.sender, delta, value);
        return value;
    }

    function reset() external {
        value = 0;
        lastCaller = msg.sender;
        emit Reset(msg.sender);
    }
}
