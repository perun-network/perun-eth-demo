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
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/pkg/errors"

	echannel "perun.network/go-perun/backend/ethereum/channel"
	ewallet "perun.network/go-perun/backend/ethereum/wallet"
	pkeystore "perun.network/go-perun/backend/ethereum/wallet/keystore"
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
	if backend, err = newNode(); err != nil {
		log.WithError(err).Fatalln("Could not initialize node.")
	}

}

func newNode() (*node, error) {
	wallet, acc, err := importAccount(config.SecretKey)
	if err != nil {
		return nil, errors.WithMessage(err, "importing secret key")
	}
	dialer := simple.NewTCPDialer(config.Node.DialTimeout)
	signer := types.NewEIP155Signer(big.NewInt(1337))

	n := &node{
		log:     log.Get(),
		onChain: acc,
		wallet:  wallet,
		dialer:  dialer,
		cb:      echannel.NewContractBackend(ethereumBackend, pkeystore.NewTransactor(*wallet, signer)),
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

	n.offChain = n.wallet.NewAccount()
	n.log.WithField("off-chain", n.offChain.Address()).Info("Generating account")

	n.bus = wirenet.NewBus(n.onChain, n.dialer)

	var err error
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
	var adjAddr common.Address
	var assAddr common.Address
	var err error

	fmt.Println("💭 Validating contracts...")

	switch contractSetup := config.Chain.contractSetup; contractSetup {
	case contractSetupOptionValidate:
		if adjAddr, err = validateAdjudicator(n.cb); err == nil { // validate adjudicator
			assAddr, err = validateAssetHolder(n.cb, adjAddr) // validate asset holder
		}
	case contractSetupOptionDeploy:
		if adjAddr, err = n.deployAdjudicator(n.cb); err == nil { // deploy adjudicator
			assAddr, err = n.deployAssetHolder(n.cb, adjAddr) // deploy asset holder
		}
	case contractSetupOptionValidateOrDeploy:
		if adjAddr, err = validateAdjudicator(n.cb); err != nil { // validate adjudicator
			fmt.Println("❌ Adjudicator invalid")
			adjAddr, err = n.deployAdjudicator(n.cb) // deploy adjudicator
		}

		if err == nil {
			if assAddr, err = validateAssetHolder(n.cb, adjAddr); err != nil { // validate asset holder
				fmt.Println("❌ Asset holder invalid")
				assAddr, err = n.deployAssetHolder(n.cb, adjAddr) // deploy asset holder
			}
		}
	default:
		// unsupported setup method
		err = errors.New(fmt.Sprintf("Unsupported contract setup method '%s'.", contractSetup))
	}

	fmt.Println("✅ Contracts validated.")

	if err != nil {
		return errors.WithMessage(err, "contract setup failed")
	}

	n.adjAddr = adjAddr
	n.assetAddr = assAddr
	recvAddr := ewallet.AsEthAddr(n.onChain.Address())
	acc := n.onChain.Account
	n.adjudicator = echannel.NewAdjudicator(n.cb, n.adjAddr, recvAddr, acc)
	n.asset = (*ewallet.Address)(&n.assetAddr)
	n.log.WithField("Adj", n.adjAddr).WithField("Asset", n.assetAddr).Debug("Set contracts")

	accounts := map[echannel.Asset]accounts.Account{ewallet.Address(n.assetAddr): acc}
	depositors := map[echannel.Asset]echannel.Depositor{ewallet.Address(n.assetAddr): new(echannel.ETHDepositor)}
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

func validateAdjudicator(cb echannel.ContractBackend) (common.Address, error) {
	ctx, cancel := newTransactionContext()
	defer cancel()

	adjAddr := config.Chain.adjudicator
	return adjAddr, echannel.ValidateAdjudicator(ctx, cb, adjAddr)
}

func validateAssetHolder(cb echannel.ContractBackend, adjAddr common.Address) (common.Address, error) {
	ctx, cancel := newTransactionContext()
	defer cancel()

	assAddr := config.Chain.assetholder
	return assAddr, echannel.ValidateAssetHolderETH(ctx, cb, assAddr, adjAddr)
}

// deployAdjudicator deploys the Adjudicator to the blockchain and returns its address
// or an error.
func (n *node) deployAdjudicator(cb echannel.ContractBackend) (common.Address, error) {
	fmt.Println("🌐 Deploying adjudicator")
	ctx, cancel := context.WithTimeout(context.Background(), config.Chain.TxTimeout)
	defer cancel()
	adjAddr, err := echannel.DeployAdjudicator(ctx, cb, n.onChain.Account)
	return adjAddr, errors.WithMessage(err, "deploying eth adjudicator")
}

// deployAssetHolder deploys the Assetholder to the blockchain and returns its address
// or an error. Needs an Adjudicator address as second argument.
func (n *node) deployAssetHolder(cb echannel.ContractBackend, adjudicator common.Address) (common.Address, error) {
	fmt.Println("🌐 Deploying asset holder")
	ctx, cancel := context.WithTimeout(context.Background(), config.Chain.TxTimeout)
	defer cancel()
	asset, err := echannel.DeployETHAssetholder(ctx, cb, adjudicator, n.onChain.Account)
	return asset, errors.WithMessage(err, "deploying eth assetholder")
}

// importAccount is a helper method to import secret keys until we have the ethereum wallet done.
func importAccount(secret string) (*pkeystore.Wallet, *pkeystore.Account, error) {
	ks := keystore.NewKeyStore(config.WalletPath, 2, 1)
	sk, err := crypto.HexToECDSA(secret[2:])
	if err != nil {
		return nil, nil, errors.WithMessage(err, "decoding secret key")
	}
	var ethAcc accounts.Account
	addr := crypto.PubkeyToAddress(sk.PublicKey)
	if ethAcc, err = ks.Find(accounts.Account{Address: addr}); err != nil {
		ethAcc, err = ks.ImportECDSA(sk, "")
		if err != nil && errors.Cause(err).Error() != "account already exists" {
			return nil, nil, errors.WithMessage(err, "importing secret key")
		}
	}

	wallet, err := pkeystore.NewWallet(ks, "")
	if err != nil {
		return nil, nil, errors.WithMessage(err, "creating wallet")
	}

	wAcc := pkeystore.NewAccountFromEth(wallet, &ethAcc)
	acc, err := wallet.Unlock(wAcc.Address())
	return wallet, acc.(*pkeystore.Account), err
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
			"ETHAssetHolder: %s\n"+
			"Adjudicator: %s\n"+
			"", config.Alias, config.Node.IP, config.Node.Port, config.Chain.URL, n.onChain.Address().String(), n.offChain.Address().String(), n.assetAddr.String(), n.adjAddr.String())

	fmt.Println("Known peers:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', tabwriter.TabIndent)
	for alias, peer := range config.Peers {
		fmt.Fprintf(w, "%s\t%v\t%s:%d\n", alias, peer.PerunID, peer.Hostname, peer.Port)
	}
	return w.Flush()
}
