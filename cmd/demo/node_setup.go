// Copyright (c) 2020 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	hdwallet "github.com/miguelmota/go-ethereum-hdwallet"
	"github.com/pkg/errors"

	"perun.network/go-perun/backend/ethereum/channel"
	echannel "perun.network/go-perun/backend/ethereum/channel"
	ewallet "perun.network/go-perun/backend/ethereum/wallet"
	phd "perun.network/go-perun/backend/ethereum/wallet/hd"
	"perun.network/go-perun/channel/persistence/keyvalue"
	"perun.network/go-perun/client"
	"perun.network/go-perun/log"
	"perun.network/go-perun/pkg/sortedkv/leveldb"
	wirenet "perun.network/go-perun/wire/net"
	"perun.network/go-perun/wire/net/simple"
)

var (
	backend         *node
	ethereumBackend *ethclient.Client
)

// Setup initializes the node, can not be done in init() since it needs the
// configuration from viper.
func Setup() {
	SetConfig()

	var err error
	if ethereumBackend, err = ethclient.Dial(config.Chain.URL); err != nil {
		log.WithError(err).Fatalln("Could not connect to ethereum node.")
	}
	channel.GasLimit = config.Chain.GasLimit
	if config.Chain.GasPrice >= 0 {
		channel.SetGasPrice(config.Chain.GasPrice)
	}
	if backend, err = newNode(); err != nil {
		log.WithError(err).Fatalln("Could not initialize node.")
	}
}

func newNode() (*node, error) {
	wallet, acc, err := setupWallet(config.Mnemonic, config.AccountIndex)
	if err != nil {
		return nil, errors.WithMessage(err, "importing mnemonic")
	}
	dialer := simple.NewTCPDialer(config.Node.DialTimeout)

	ctx, cancel := context.WithTimeout(context.Background(), config.Node.HandleTimeout)
	defer cancel()
	chainID, err := ethereumBackend.NetworkID(ctx)
	if err != nil {
		return nil, errors.WithMessage(err, "checking chainID")
	}
	configChainID := big.NewInt(config.Chain.ID)
	if chainID.Cmp(configChainID) != 0 {
		return nil, errors.New("Endpoint returned different chain ID: " + chainID.String())
	}
	signer := types.NewEIP155Signer(big.NewInt(config.Chain.ID))

	n := &node{
		log:     log.Get(),
		onChain: acc,
		wallet:  wallet,
		dialer:  dialer,
		cb:      echannel.NewContractBackend(ethereumBackend, phd.NewTransactor(wallet.Wallet(), signer)),
		assets:  make(map[string]common.Address),
		peers:   make(map[string]*peer),
	}
	return n, n.setup()
}

// setup does:
//  - Create a new offChain account.
//  - Create a client with the node's dialer, funder, adjudicator and wallet.
//  - Setup a TCP listener for incoming connections.
//  - Load or create the database and setting up persistence with it.
//  - Set the OnNewChannel, Proposal and Update handler.
//  - Print the configuration.
func (n *node) setup() error {
	if err := n.setupContracts(); err != nil {
		return errors.WithMessage(err, "setting up contracts")
	}

	var err error

	n.offChain, err = n.wallet.NewAccount()
	if err != nil {
		return errors.WithMessage(err, "creating account")
	}

	n.log.WithField("off-chain", n.offChain.Address()).Info("Generating account")

	n.bus = wirenet.NewBus(n.onChain, n.dialer)

	if n.client, err = client.New(n.onChain.Address(), n.bus, n.funder, n.adjudicator, n.wallet); err != nil {
		return errors.WithMessage(err, "creating client")
	}

	host := config.Node.IP + ":" + strconv.Itoa(int(config.Node.Port))
	n.log.WithField("host", host).Trace("Listening for connections")
	listener, err := simple.NewTCPListener(host)
	if err != nil {
		return errors.WithMessage(err, "could not start tcp listener")
	}

	n.client.OnNewChannel(n.setupChannel)
	if err := n.setupPersistence(); err != nil {
		return errors.WithMessage(err, "setting up persistence")
	}
	go n.client.Handle(n, n)
	go n.bus.Listen(listener)
	n.PrintConfig()
	return nil
}

func (n *node) setupContracts() error {
	depositors := make(map[echannel.Asset]echannel.Depositor)

	switch contractSetup := config.Contracts.Deployment; contractSetup {
	case contractSetupOptionValidate:
		adjAddr := config.Contracts.Adjudicator
		// Validate adjudicator
		if err := validateAdjudicator(n.cb, adjAddr); err != nil {
			return errors.WithMessage(err, "validating adjudicator")
		}
		n.adjAddr = adjAddr

		// Validate all AssetHolders
		for name, asset := range config.Contracts.Assets {
			var err error
			switch asset.Type {
			case assetTypeEth:
				err = validateAssetHolderETH(n.cb, adjAddr, asset.Assetholder)
				depositors[ewallet.Address(asset.Assetholder)] = new(echannel.ETHDepositor)
			case assetTypeErc20:
				err = validateAssetHolderERC20(n.cb, adjAddr, asset.Assetholder, asset.Address)
				if err == nil {
					err = validatePerunToken(n.cb, asset.Address)
				}
				depositors[ewallet.Address(asset.Assetholder)] = &echannel.ERC20Depositor{Token: asset.Address}
			default:
				err = errors.Errorf("invalid asset type: %s", asset.Type)
			}
			if err != nil {
				return errors.WithMessagef(err, "validating asset: %s", name)
			}
			n.assets[name] = asset.Assetholder
		}
		fmt.Println("✅ Contracts validated.")
	case contractSetupOptionDeploy:
		// Deploy Adjudicator
		var err error
		config.Contracts.Adjudicator, err = deployAdjudicator(n.cb, n.onChain.Account)
		if err != nil {
			return errors.WithMessage(err, "deploying adjudicator")
		}
		n.adjAddr = config.Contracts.Adjudicator

		// Deploy all AssetHolders
		for name, asset := range config.Contracts.Assets {
			var err error

			switch asset.Type {
			case assetTypeEth:
				asset.Assetholder, err = deployAssetHolderETH(n.cb, n.onChain.Account, n.adjAddr)
				depositors[ewallet.Address(asset.Assetholder)] = new(echannel.ETHDepositor)
			case assetTypeErc20:
				// Prefund all peers from the network config with ERC20 tokens.
				asset.Address, err = deployPerunToken(n.cb, n.onChain.Account, config.peerAddresses()...)
				if err == nil {
					asset.Assetholder, err = deployAssetHolderERC20(n.cb, n.onChain.Account, n.adjAddr, asset.Address)
					depositors[ewallet.Address(asset.Assetholder)] = &echannel.ERC20Depositor{Token: asset.Assetholder}
				}
			default:
				log.Panicf("invalid asset type: %v", asset.Type)
			}
			if err != nil {
				return errors.WithMessagef(err, "validating asset: %s", name)
			}
			n.assets[name] = asset.Assetholder
		}
		fmt.Println("✅ Contracts deployed.")
	case contractSetupOptionNone:
		// Set adjudicator
		n.adjAddr = config.Contracts.Adjudicator
		// Set all depositors
		for name, asset := range config.Contracts.Assets {
			switch asset.Type {
			case assetTypeEth:
				depositors[ewallet.Address(asset.Assetholder)] = new(echannel.ETHDepositor)
			case assetTypeErc20:
				depositors[ewallet.Address(asset.Assetholder)] = &echannel.ERC20Depositor{Token: asset.Address}
			default:
				log.Panicf("invalid asset type: %v", asset.Type)
			}
			n.assets[name] = asset.Assetholder
		}
		fmt.Println("✅ Contracts set.")
	default:
		return errors.New(fmt.Sprintf("Unsupported contract setup method '%s'.", contractSetup))
	}

	withdrawAddr := ewallet.AsEthAddr(n.onChain.Address())
	n.adjudicator = echannel.NewAdjudicator(n.cb, n.adjAddr, withdrawAddr, n.onChain.Account)

	accounts := make(map[echannel.Asset]accounts.Account)
	for _, asset := range n.assets {
		accounts[ewallet.Address(asset)] = n.onChain.Account
	}
	n.funder = echannel.NewFunder(n.cb, accounts, depositors)

	return nil
}

func (n *node) setupPersistence() error {
	if config.Node.PersistenceEnabled {
		n.log.Info("Starting persistence")
		db, err := leveldb.LoadDatabase(config.Node.PersistencePath)
		if err != nil {
			return errors.WithMessage(err, "creating/loading database")
		}
		persister := keyvalue.NewPersistRestorer(db)
		n.client.EnablePersistence(persister)

		ctx, cancel := context.WithTimeout(context.Background(), config.Node.ReconnecTimeout)
		defer cancel()
		if err := n.client.Restore(ctx); err != nil {
			n.log.WithError(err).Warn("Could not restore client")
			// return the error.
		}
	} else {
		n.log.Info("Persistence disabled")
	}
	return nil
}

func validateAdjudicator(cb echannel.ContractBackend, adjAddr common.Address) error {
	fmt.Println("💭 Validating Adjudicator at: ", adjAddr.Hex())
	ctx, cancel := newTransactionContext()
	defer cancel()

	return echannel.ValidateAdjudicator(ctx, cb, adjAddr)
}

func validateAssetHolderETH(cb echannel.ContractBackend, adjAddr, assAddr common.Address) error {
	fmt.Println("💭 Validating AssetholderETH at: ", assAddr)
	ctx, cancel := newTransactionContext()
	defer cancel()

	return echannel.ValidateAssetHolderETH(ctx, cb, assAddr, adjAddr)
}

func validateAssetHolderERC20(cb echannel.ContractBackend, adjAddr, assAddr, token common.Address) error {
	fmt.Println("💭 Validating AssetholderERC20 at: ", assAddr)
	ctx, cancel := newTransactionContext()
	defer cancel()

	return echannel.ValidateAdjudicator(ctx, cb, adjAddr)
}

func validatePerunToken(cb echannel.ContractBackend, tokenAddr common.Address) error {
	fmt.Println("💭 Validating PerunToken at: ", tokenAddr)
	ctx, cancel := newTransactionContext()
	defer cancel()

	return echannel.ValidatePerunToken(ctx, cb, tokenAddr)
}

// deployAdjudicator deploys the Adjudicator to the blockchain and returns its address
// or an error.
func deployAdjudicator(cb echannel.ContractBackend, acc accounts.Account) (common.Address, error) {
	fmt.Println("🌐 Deploying adjudicator")
	ctx, cancel := context.WithTimeout(context.Background(), config.Chain.TxTimeout)
	defer cancel()
	adjAddr, err := echannel.DeployAdjudicator(ctx, cb, acc)
	return adjAddr, errors.WithMessage(err, "deploying eth adjudicator")
}

// deployAssetHolder deploys the Assetholder to the blockchain and returns its address
// or an error. Needs an Adjudicator address as second argument.
func deployAssetHolderETH(cb echannel.ContractBackend, acc accounts.Account, adjudicator common.Address) (common.Address, error) {
	fmt.Println("🌐 Deploying asset holder")
	ctx, cancel := context.WithTimeout(context.Background(), config.Chain.TxTimeout)
	defer cancel()
	asset, err := echannel.DeployETHAssetholder(ctx, cb, adjudicator, acc)
	return asset, errors.WithMessage(err, "deploying eth assetholder")
}

func deployAssetHolderERC20(cb echannel.ContractBackend, acc accounts.Account, adjudicator, tokenAddr common.Address) (common.Address, error) {
	fmt.Println("🌐 Deploying asset holder")
	ctx, cancel := context.WithTimeout(context.Background(), config.Chain.TxTimeout)
	defer cancel()
	asset, err := echannel.DeployERC20Assetholder(ctx, cb, adjudicator, tokenAddr, acc)
	return asset, errors.WithMessage(err, "deploying erc20 assetholder")
}

func deployPerunToken(cb echannel.ContractBackend, acc accounts.Account, prefunded ...common.Address) (common.Address, error) {
	fmt.Println("🌐 Deploying perun token")
	ctx, cancel := context.WithTimeout(context.Background(), config.Chain.TxTimeout)
	defer cancel()
	initBal, _ := new(big.Int).SetString("1000000000000000000000", 10)
	asset, err := echannel.DeployPerunToken(ctx, cb, acc, prefunded, initBal)
	return asset, errors.WithMessage(err, "deploying perun token")
}

// setupWallet imports the mnemonic and returns a corresponding wallet and
// the derived account at the given account index.
func setupWallet(mnemonic string, accountIndex uint) (*phd.Wallet, *phd.Account, error) {
	wallet, err := hdwallet.NewFromMnemonic(mnemonic)
	if err != nil {
		return nil, nil, errors.WithMessage(err, "creating hdwallet")
	}

	perunWallet, err := phd.NewWallet(wallet, accounts.DefaultBaseDerivationPath.String(), accountIndex)
	if err != nil {
		return nil, nil, errors.WithMessage(err, "creating perun wallet")
	}
	acc, err := perunWallet.NewAccount()
	if err != nil {
		return nil, nil, errors.WithMessage(err, "creating account")
	}

	return perunWallet, acc, nil
}

func newTransactionContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), config.Chain.TxTimeout)
}

func (n *node) PrintConfig() error {
	fmt.Printf(
		"Alias: %s\n"+
			"Listening: %s:%d\n"+
			"ETH RPC URL: %s\n"+
			"Perun ID: %s\n"+
			"OffChain: %s\n"+
			"Adjudicator: %s\n"+
			"Assets:\n", config.Alias, config.Node.IP, config.Node.Port, config.Chain.URL, n.onChain.Address().String(), n.offChain.Address().String(), n.adjAddr.String())
	for name, asset := range n.assets {
		fmt.Printf("\t%s at %s\n", name, asset.Hex())
	}

	fmt.Println("Known peers:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', tabwriter.TabIndent)
	for alias, peer := range config.Peers {
		fmt.Fprintf(w, "%s\t%v\t%s:%d\n", alias, peer.PerunID, peer.Hostname, peer.Port)
	}
	return w.Flush()
}
