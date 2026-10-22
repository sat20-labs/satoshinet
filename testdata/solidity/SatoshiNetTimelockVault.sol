// SPDX-License-Identifier: MIT
pragma solidity ^0.8.19;

interface ISatoshiNetAsset {
    function transferAsset(string calldata assetName, string calldata to, string calldata amount, bytes calldata extraData) external returns (bool);
}

interface ISatoshiNetTrigger {
    function registerHeightTrigger(string calldata id, uint256 height, uint256 gasLimit, bytes calldata callData) external returns (bool);
}

// SatoshiNetTimelockVault verifies the SatoshiNet-specific EVM extensions:
// constructor-time trigger registration, height-trigger execution, and native
// multi-asset settlement through the asset precompile.
contract SatoshiNetTimelockVault {
    ISatoshiNetAsset constant ASSET = ISatoshiNetAsset(0x0000000000000000000000000000000000534E01);
    ISatoshiNetTrigger constant TRIGGER = ISatoshiNetTrigger(0x0000000000000000000000000000000000534e02);

    string public assetName;
    string public recipient;
    string public amount;
    uint256 public releaseHeight;
    bool public released;

    event Released(string assetName, string recipient, string amount);

    constructor(string memory assetName_, string memory recipient_, string memory amount_, uint256 releaseHeight_) {
        require(bytes(assetName_).length != 0, "asset");
        require(bytes(recipient_).length != 0, "recipient");
        require(bytes(amount_).length != 0, "amount");
        require(releaseHeight_ > block.number, "height");
        assetName = assetName_;
        recipient = recipient_;
        amount = amount_;
        releaseHeight = releaseHeight_;

        bytes memory callData = abi.encodeWithSelector(this.release.selector);
        require(TRIGGER.registerHeightTrigger("vault-release", releaseHeight_, 800000, callData), "trigger");
    }

    function release() external {
        require(!released, "released");
        require(block.number >= releaseHeight, "not due");
        released = true;
        require(ASSET.transferAsset(assetName, recipient, amount, ""), "transfer");
        emit Released(assetName, recipient, amount);
    }
}
