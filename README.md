# Perun CLI

This folder contains a proof-of-concept implementation of a CLI payment-channel
node.

## Demo

The currently only sub-command is `demo`, which starts the CLI node. The node's
configuration file can be chosen with the `--config` flag. Two sample
configurations `alice.yaml` and `bob.yaml` are provided. A default network
configuration for Alice and Bob is provided in file `network.yaml`.

## Example Walkthrough
In a first terminal, start a `ganache-cli` development blockchain, prefunding
the accounts of `Alice` and `Bob`:
```
ganache-cli --account="0x7d51a817ee07c3f28581c47a5072142193337fdca4d7911e58c5af2d03895d1a,100000000000000000000000" --account="0x6aeeb7f09e757baa9d3935a042c3d0d46a2eda19e9b676283dce4eaf32e29dc9,100000000000000000000000"
```

In a second and third terminal, cd inside folder `perun` of `go-perun` and start
the nodes of Alice and Bob with
```
go run main.go demo --config alice.yaml
```
and
```
go run main.go demo --config bob.yaml
```
You can see two transaction in the ganache terminal, which correspond to the
deployment of the `AssetHolder` and `Adjudicator` contracts.

Once both CLIs are running, e.g. in Alice's terminal, connect to bob with
```
> connect bob
```
Then open a payment channel with 100 ETH deposit from both sides with
```
> open bob 100 100
```
In the ganache terminal, you can see two new transactions, which correspond to
the funding transactions by Alice and Bob.

Now you can execute off-chain payments, e.g. in Bob's terminal with
```
> send alice 10
```
The updated balance will immediately be printed in both terminals, but no
transaction will be visible in the ganache's terminal.

You may always check the current status with command `info`.

You can also run a performance benchmark with command
```
> benchmark alice 1000
```
which will benchmark 1000 transactions without updating the payment channel
balances. The results will be printed in a table.

Finally, you can settle the channel on either side with
```
> close alice
```
which will send one `concludeFinal` and two withdrawal transactions to the
ganache blockchain.

Now you can exit the CLI with command `exit`.
