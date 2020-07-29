// Copyright (c) 2020 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/pkg/errors"

	"perun.network/go-perun/apps/payment"
	echannel "perun.network/go-perun/backend/ethereum/channel"
	ewallet "perun.network/go-perun/backend/ethereum/wallet"
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

	appDef := &ewallet.Address{} // dummy app def
	payment.SetAppDef(appDef)

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

	n := &node{
		log:     log.Get(),
		onChain: acc,
		wallet:  wallet,
		dialer:  dialer,
		cb:      echannel.NewContractBackend(ethereumBackend, wallet.Ks, &acc.Account),
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

// setupContracts reads from the config file whether the node should deploy or use
// existing contract addresses.
func (n *node) setupContracts() (err error) {
	var adjAddr, assAddr common.Address

	if config.Chain.Adjudicator == "deploy" {
		adjAddr, err = deployAdjudicator(n.cb)
		if err != nil {
			return
		}
		fmt.Println("💭 Adjudicator contract deployed")
	} else {
		tmpAdj, err := strToAddress(config.Chain.Adjudicator)
		if err != nil {
			return err
		}
		adjAddr = ewallet.AsEthAddr(tmpAdj)

		ctx, cancel := context.WithTimeout(context.Background(), config.Chain.TxTimeout)
		defer cancel()
		if err := echannel.ValidateAdjudicator(ctx, n.cb, adjAddr); err != nil {
			return errors.WithMessage(err, "validating adjudicator contract")
		}

		fmt.Println("💭 Adjudicator contract validated")
	}

	if config.Chain.Assetholder == "deploy" {
		assAddr, err = deployAsset(n.cb, adjAddr)
		if err != nil {
			return
		}
		fmt.Println("💭 Asset holder contract deployed")
	} else {
		tmpAsset, err := strToAddress(config.Chain.Assetholder)
		if err != nil {
			return err
		}
		assAddr = ewallet.AsEthAddr(tmpAsset)

		ctx, cancel := context.WithTimeout(context.Background(), config.Chain.TxTimeout)
		defer cancel()
		if err := echannel.ValidateAssetHolderETH(ctx, n.cb, assAddr, adjAddr); err != nil {
			return errors.WithMessage(err, "validating asset holder contract")
		}

		fmt.Println("💭 Asset holder contract validated")
	}
	n.adjAddr = adjAddr
	n.assetAddr = assAddr

	recvAddr := ewallet.AsEthAddr(n.onChain.Address())
	n.adjudicator = echannel.NewAdjudicator(n.cb, adjAddr, recvAddr)
	n.funder = echannel.NewETHFunder(n.cb, assAddr)
	n.asset = (*ewallet.Address)(&assAddr)
	n.log.WithField("Adj", adjAddr).WithField("Asset", assAddr).Debug("Set contracts")
	return
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

// deployAdjudicator deploys the Adjudicator to the blockchain and returns its address
// or an error.
func deployAdjudicator(cb echannel.ContractBackend) (common.Address, error) {
	ctx, cancel := context.WithTimeout(context.Background(), config.Chain.TxTimeout)
	defer cancel()
	adjAddr, err := echannel.DeployAdjudicator(ctx, cb)
	return adjAddr, errors.WithMessage(err, "deploying eth adjudicator")
}

// deployAsset deploys the Assetholder to the blockchain and returns its address
// or an error. Needs an Adjudicator address as second argument.
func deployAsset(cb echannel.ContractBackend, adjudicator common.Address) (common.Address, error) {
	ctx, cancel := context.WithTimeout(context.Background(), config.Chain.TxTimeout)
	defer cancel()
	asset, err := echannel.DeployETHAssetholder(ctx, cb, adjudicator)
	return asset, errors.WithMessage(err, "deploying eth assetholder")
}

// importAccount is a helper method to import secret keys until we have the ethereum wallet done.
func importAccount(secret string) (*ewallet.Wallet, *ewallet.Account, error) {
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

	wallet, err := ewallet.NewWallet(ks, "")
	if err != nil {
		return nil, nil, errors.WithMessage(err, "creating wallet")
	}

	wAcc := ewallet.NewAccountFromEth(wallet, &ethAcc)
	acc, err := wallet.Unlock(wAcc.Address())
	return wallet, acc.(*ewallet.Account), err
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
