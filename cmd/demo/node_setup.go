// Copyright (c) 2020 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	dotclient "github.com/perun-network/perun-polkadot-backend/client"
	dotwallet "github.com/perun-network/perun-polkadot-backend/wallet/sr25519"

	"github.com/pkg/errors"
	"perun.network/go-perun/channel/persistence/keyvalue"
	"perun.network/go-perun/client"
	"perun.network/go-perun/log"
	"perun.network/go-perun/pkg/sortedkv/leveldb"
	wirenet "perun.network/go-perun/wire/net"
	"perun.network/go-perun/wire/net/simple"
)

var backend *node

// Setup initializes the node, can not be done in init() since it needs the
// configuration from viper.
func Setup() {
	SetConfig(flags.cfgFile, flags.cfgNetFile)

	var err error
	if backend, err = newNode(); err != nil {
		log.WithError(err).Fatalln("Could not initialize node.")
	}
}

func newNode() (*node, error) {
	wallet, acc, err := setupWallet(config.Sk)
	if err != nil {
		return nil, errors.WithMessage(err, "importing mnemonic")
	}
	dot, err := dotclient.NewSetup(acc, config.Chain)
	if err != nil {
		return nil, errors.WithMessage(err, "creating dot setup")
	}
	dialer := simple.NewTCPDialer(config.Node.DialTimeout)

	n := &node{
		log:         log.Get(),
		onChain:     acc,
		wallet:      wallet,
		api:         dot.Api,
		adjudicator: dot.Adjudicator,
		funder:      dot.Funder,
		dialer:      dialer,
		peers:       make(map[string]*peer),
	}
	return n, n.setup()
}

func (n *node) setup() error {
	var err error

	n.offChain, err = n.wallet.Generate(rand.Reader)
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

func (n *node) setupPersistence() error {
	if config.Node.PersistenceEnabled {
		n.log.Info("Starting persistence")
		db, err := leveldb.LoadDatabase(config.Node.PersistencePath)
		if err != nil {
			return errors.WithMessage(err, "creating/loading database")
		}
		persister := keyvalue.NewPersistRestorer(db)
		n.client.EnablePersistence(persister)

		ctx, cancel := context.WithTimeout(context.Background(), config.Node.ReconnectTimeout)
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

// setupWallet imports the mnemonic and returns a corresponding wallet and
// the derived account at the given account index.
func setupWallet(hexPk string) (*dotwallet.Wallet, *dotwallet.Account, error) {
	perunWallet, err := dotwallet.NewWallet()
	if err != nil {
		return nil, nil, errors.WithMessage(err, "creating perun wallet")
	}
	sk, err := dotwallet.NewSkFromHex(hexPk)
	if err != nil {
		return nil, nil, errors.WithMessage(err, "creating hdwallet")
	}
	acc := perunWallet.ImportSK(sk)
	return perunWallet, acc, nil
}

func (n *node) PrintConfig() error {
	info, _ := n.api.Info()
	fmt.Printf(
		"Alias: %s\n"+
			"Listening: %s:%d\n"+
			"ETH RPC URL: %s\n"+
			"Perun ID: %s\n"+
			"OffChain: %s\n"+
			"Node info: %s\n"+
			"", config.Alias, config.Node.IP, config.Node.Port, config.Chain.NodeUrl, n.onChain.Address().String(), n.offChain.Address().String(), info)

	fmt.Println("Known peers:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', tabwriter.TabIndent)
	for alias, peer := range config.Peers {
		fmt.Fprintf(w, "%s\t%v\t%s:%d\n", alias, peer.PerunID, peer.Hostname, peer.Port)
	}
	return w.Flush()
}
