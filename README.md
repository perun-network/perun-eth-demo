<h1 align="center"><br>
    <a href="https://perun.network/"><img src=".assets/logo.png" alt="Perun" width="196"></a>
<br></h1>

<h4 align="center">Perun Ethereum Demo CLI</h4>

<p align="center">
  <a href="https://goreportcard.com/report/github.com/perun-network/perun-eth-demo"><img src="https://goreportcard.com/badge/github.com/perun-network/perun-eth-demo" alt="Goreportcard status"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache%202.0-blue.svg" alt="License: Apache 2.0"></a>
  <a href="https://github.com/perun-network/perun-eth-demo/actions"><img src="https://github.com/perun-network/perun-eth-demo/workflows/Testing/badge.svg" alt="CI Testing status"></a>
</p>

_perun-eth-demo_ allows you to interact with [perun](https://perun.network/) Payment-Channels over a CLI powered by [go-perun](https://github.com/perun-network/go-perun).  
You can open a Payment-Channel, send off-chain payments and close it, whereby all interaction with the Ethereum blockchain is handled by _go-perun_. Give it a try and be amazed by [Perun Network](https://perun.network/) :rocket: :moon: !

## Security Disclaimer
The authors take no responsibility for any loss of digital assets or other damage caused by the use of this software.  
**Do not use this software with real funds**.

## Getting Started

Running _perun-eth-demo_ requires [Go 1.15](https://golang.org) or higher. To follow the walkthrough we recommend to also install [ganache-cli](https://github.com/trufflesuite/ganache-cli), but _perun-eth-demo_ works with any ethereum node.
```sh
# Clone the repository into a directory of your choice
git clone https://github.com/perun-network/perun-eth-demo
cd perun-eth-demo
# Compile with
go build
# Check that the binary works
./perun-eth-demo
```

## Contracts

To deploy the contracts follow the [README](https://github.com/perun-network/perun-eth-contracts/tree/scaling-eth-21) of the contracts repo.

Copy the addresses that are printed into the files `alice.yaml` and `bob.yaml` under section `contracts`.  
Ensure that the `contracts.deployment` value is `none` since contracts that are deployed
with hardhat can not be validated.

## Demo

Currently, the only available sub-command of _perun-eth-demo_ is `demo`, which starts the CLI node. The node's
configuration file can be chosen with the `--config` flag. Two sample
configurations `alice.yaml` and `bob.yaml` are provided. A default network
configuration for Alice and Bob is provided in file `network.yaml`.

## Example Walkthrough - Arbitrum

1. In a first terminal, start an `arbitrum` development blockchain.  
Follow [this](https://developer.offchainlabs.com/docs/local_blockchain) tutorial or use the public Kovan4 test-net with infura.

2. Open a terminal, start the node of Alice with
```sh
./perun-eth-demo demo --config alice.yaml --chain arbitrum_kovan
```

3. In a second terminal, start the node of Bob with
```sh
./perun-eth-demo demo --config bob.yaml --chain arbitrum_kovan
```

Once both CLIs are running, e.g. in Alice's terminal, propose a payment channel
to Bob with 10 *PerunToken* deposit from both sides via the following command.
```
> open bob peruntoken 10 10
```
In Alice's terminal, accept the appearing channel proposal.
```
🔁 Incoming channel proposal from bob with peruntoken funding [My: 10, Peer: 10].
Accept (y/n)? > y
```
The terminal will print the hashes of two transaction: *IncreaseAllowance* and *Deposit*.

Now you can execute off-chain payments, e.g. in Bob's terminal with
```
> send alice 1
```
The updated balance will immediately be printed in both terminals, but no
transaction will be visible on-chain.

You may always check the current status with command `info`.

You can also run a performance benchmark with command
```
> benchmark alice 5 100
```
which will send 5 *PerunToken* in 100 micro-transactions from Bob to Alice. Transaction performance will be printed in a table.

Finally, you can settle the channel on either side with
```
> close alice
```
which will send one `concludeFinal` and two withdrawal transactions to the chain.

Now you can exit the CLI with command `exit` or `Ctrl+D`.

## Copyright

Copyright &copy; 2021 Chair of Applied Cryptography, Technische Universität Darmstadt, Germany.
All rights reserved.
Use of the source code is governed by the Apache 2.0 license that can be found in the [LICENSE file](LICENSE).

Contact us at [info@perun.network](mailto:info@perun.network).
