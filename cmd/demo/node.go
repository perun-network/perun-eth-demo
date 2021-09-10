// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
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
	"sync"
	"text/tabwriter"
	"time"

	"github.com/pkg/errors"

	dotchannel "github.com/perun-network/perun-polkadot-backend/channel"
	dotclient "github.com/perun-network/perun-polkadot-backend/client"
	dotsubstrate "github.com/perun-network/perun-polkadot-backend/pkg/substrate"
	dotwallet "github.com/perun-network/perun-polkadot-backend/wallet/sr25519"
	"perun.network/go-perun/channel"
	"perun.network/go-perun/client"
	"perun.network/go-perun/log"
	"perun.network/go-perun/wallet"
	"perun.network/go-perun/wire"
	wirenet "perun.network/go-perun/wire/net"
	"perun.network/go-perun/wire/net/simple"
)

type peer struct {
	alias   string
	perunID wire.Address
	ch      *paymentChannel
	log     log.Logger
}

type node struct {
	log log.Logger

	bus    *wirenet.Bus
	client *client.Client
	dialer *simple.Dialer
	api    *dotsubstrate.Api

	// Account for signing on-chain TX. Currently also the Perun-ID.
	onChain *dotwallet.Account
	// Account for signing off-chain TX. Currently one Account for all
	// state channels, later one we want one Account per Channel.
	offChain wallet.Account
	wallet   *dotwallet.Wallet

	adjudicator channel.Adjudicator
	funder      channel.Funder

	// Protects peers
	mtx   sync.Mutex
	peers map[string]*peer
}

func (n *node) getOnChainBal(ctx context.Context, addrs ...wallet.Address) ([]*big.Int, error) {
	bals := make([]*big.Int, len(addrs))
	for i, addr := range addrs {
		accInfo, err := n.api.AccountInfo(dotwallet.AsAddr(addr).AccountId())
		if err != nil {
			return nil, errors.Wrap(err, "querying on-chain balance")
		}
		bals[i] = accInfo.Free.Int
	}
	return bals, nil
}

func (n *node) Connect(args []string) error {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	return n.connect(args[0])
}

func (n *node) connect(alias string) error {
	n.log.Traceln("Connecting...")
	if n.peers[alias] != nil {
		return errors.New("Peer already connected")
	}
	peerCfg, ok := config.Peers[alias]
	if !ok {
		return errors.Errorf("Alias '%s' unknown. Add it to 'network.yaml'.", alias)
	}

	n.dialer.Register(peerCfg.perunID, peerCfg.Hostname+":"+strconv.Itoa(int(peerCfg.Port)))

	n.peers[alias] = &peer{
		alias:   alias,
		perunID: peerCfg.perunID,
		log:     log.WithField("peer", peerCfg.perunID),
	}

	fmt.Printf("📡 Connected to %v. Ready to open channel.\n", alias)

	return nil
}

// peer returns the peer with the address `addr` or nil if not found.
func (n *node) peer(addr wire.Address) *peer {
	for _, peer := range n.peers {
		if peer.perunID.Equals(addr) {
			return peer
		}
	}
	return nil
}

func (n *node) channelPeer(ch *client.Channel) *peer {
	perunID := ch.Peers()[1-ch.Idx()] // assumes two-party channel
	return n.peer(perunID)
}

func (n *node) setupChannel(ch *client.Channel) {
	if len(ch.Peers()) != 2 {
		log.Fatal("Only channels with two participants are currently supported")
	}

	perunID := ch.Peers()[1-ch.Idx()] // assumes two-party channel
	p := n.peer(perunID)

	if p == nil {
		log.WithField("peer", perunID).Warn("Opened channel to unknown peer")
		return
	} else if p.ch != nil {
		p.log.Warn("Peer tried to open more than one channel")
		return
	}

	p.ch = newPaymentChannel(ch)

	// Start watching.
	go func() {
		l := log.WithField("channel", ch.ID())
		l.Debug("Watcher started")
		err := ch.Watch(n)
		l.WithError(err).Debug("Watcher stopped")
	}()

	bals := dotclient.NewDotsFromPlanks(ch.State().Balances[0]...)
	fmt.Printf("🆕 Channel established with %s. Initial balance: [My: %v, Peer: %v]\n",
		p.alias, bals[ch.Idx()], bals[1-ch.Idx()]) // assumes two-party channel
}

func (n *node) HandleAdjudicatorEvent(e channel.AdjudicatorEvent) {
	if _, ok := e.(*channel.ConcludedEvent); ok {
		PrintfAsync("🎭 Received concluded event\n")
		func() {
			n.mtx.Lock()
			defer n.mtx.Unlock()
			ch := n.channel(e.ID())
			if ch == nil {
				// If we initiated the channel closing, then the channel should
				// already be removed and we return.
				return
			}
			peer := n.channelPeer(ch.Channel)
			if err := n.settle(peer); err != nil {
				PrintfAsync("🎭 error while settling: %v\n", err)
			}
			PrintfAsync("🏁 Settled channel with %s.\n", peer.alias)
		}()
	}
}

type balTuple struct {
	My, Other *big.Int
}

func (n *node) GetBals() map[string]balTuple {
	n.mtx.Lock()
	defer n.mtx.Unlock()

	bals := make(map[string]balTuple)
	for alias, peer := range n.peers {
		if peer.ch != nil {
			my, other := peer.ch.GetBalances()
			bals[alias] = balTuple{my, other}
		}
	}
	return bals
}

func findConfig(id wallet.Address) (string, *netConfigEntry) {
	for alias, e := range config.Peers {
		if e.perunID.String() == id.String() {
			return alias, e
		}
	}
	return "", nil
}

func (n *node) HandleUpdate(_ *channel.State, update client.ChannelUpdate, resp *client.UpdateResponder) {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	log := n.log.WithField("channel", update.State.ID)
	log.Debug("Channel update")

	ch := n.channel(update.State.ID)
	if ch == nil {
		log.Error("Channel for ID not found")
		return
	}
	ch.Handle(update, resp)
}

func (n *node) channel(id channel.ID) *paymentChannel {
	for _, p := range n.peers {
		if p.ch != nil && p.ch.ID() == id {
			return p.ch
		}
	}
	return nil
}

func (n *node) HandleProposal(prop client.ChannelProposal, res *client.ProposalResponder) {
	req, ok := prop.(*client.LedgerChannelProposal)
	if !ok {
		log.Fatal("Can handle only ledger channel proposals.")
	}

	if len(req.Peers) != 2 {
		log.Fatal("Only channels with two participants are currently supported")
	}

	n.mtx.Lock()
	defer n.mtx.Unlock()
	id := req.Peers[0]
	n.log.Debug("Received channel proposal")

	// Find the peer by its perunID and create it if not present
	p := n.peer(id)
	alias, cfg := findConfig(id)

	if p == nil {
		if cfg == nil {
			ctx, cancel := context.WithTimeout(context.Background(), config.Node.HandleTimeout)
			defer cancel()
			if err := res.Reject(ctx, "Unknown identity"); err != nil {
				n.log.WithError(err).Warn("rejecting")
			}
			return
		}
		p = &peer{
			alias:   alias,
			perunID: id,
			log:     log.WithField("peer", id),
		}
		n.peers[alias] = p
		n.log.WithField("channel", id).WithField("alias", alias).Debug("New peer")
	}
	n.log.WithField("peer", id).Debug("Channel proposal")

	bals := dotclient.NewDotsFromPlanks(req.InitBals.Balances[0]...)
	theirBal := bals[0] // proposer has index 0
	ourBal := bals[1]   // proposal receiver has index 1
	msg := fmt.Sprintf("🔁 Incoming channel proposal from %v with funding [My: %v, Peer: %v].\nAccept (y/n)? ", alias, ourBal, theirBal)
	Prompt(msg, func(userInput string) {
		ctx, cancel := context.WithTimeout(context.Background(), config.Node.HandleTimeout)
		defer cancel()

		if userInput == "y" {
			fmt.Printf("✅ Channel proposal accepted. Opening channel...\n")
			a := req.Accept(n.offChain.Address(), client.WithRandomNonce())
			if _, err := res.Accept(ctx, a); err != nil {
				n.log.Error(errors.WithMessage(err, "accepting channel proposal"))
				return
			}
		} else {
			fmt.Printf("❌ Channel proposal rejected\n")
			if err := res.Reject(ctx, "rejected by user"); err != nil {
				n.log.Error(errors.WithMessage(err, "rejecting channel proposal"))
				return
			}
		}
	})
}

func (n *node) Open(args []string) error {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	peerName := args[0]
	peer := n.peers[peerName]
	if peer == nil {
		// try to connect to peer
		if err := n.connect(peerName); err != nil {
			return err
		}
		peer = n.peers[peerName]
	}
	myBalEth, _ := new(big.Float).SetString(args[1]) // Input was already validated by command parser.
	peerBalEth, _ := new(big.Float).SetString(args[2])

	initBals := &channel.Allocation{
		Assets:   []channel.Asset{dotchannel.NewAsset()},
		Balances: [][]*big.Int{dotToPlank(myBalEth, peerBalEth)},
	}

	prop, err := client.NewLedgerChannelProposal(
		config.Channel.ChallengeDurationSec,
		n.offChain.Address(),
		initBals,
		[]wire.Address{n.onChain.Address(), peer.perunID},
		client.WithRandomNonce(),
	)
	if err != nil {
		return errors.WithMessage(err, "creating channel proposal")
	}

	fmt.Printf("💭 Proposing channel to %v...\n", peerName)

	ctx, cancel := context.WithTimeout(context.Background(), config.Channel.FundTimeout)
	defer cancel()
	n.log.Debug("Proposing channel")
	ch, err := n.client.ProposeChannel(ctx, prop)
	if err != nil {
		return errors.WithMessage(err, "proposing channel failed")
	}
	if n.channel(ch.ID()) == nil {
		return errors.New("OnNewChannel handler could not setup channel")
	}
	return nil
}

func (n *node) Send(args []string) error {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	n.log.Traceln("Sending...")

	peer := n.peers[args[0]]
	if peer == nil {
		return errors.Errorf("peer not found %s", args[0])
	} else if peer.ch == nil {
		return errors.Errorf("connect to peer first")
	}
	amountEth, _ := new(big.Float).SetString(args[1]) // Input was already validated by command parser.
	return peer.ch.sendMoney(dotToPlank(amountEth)[0])
}

func (n *node) Close(args []string) error {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	n.log.Traceln("Closing...")

	alias := args[0]
	peer := n.peers[alias]
	if peer == nil {
		return errors.Errorf("Unknown peer: %s", alias)
	}
	if err := peer.ch.sendFinal(); err != nil {
		return errors.WithMessage(err, "sending final state for state closing")
	}

	if err := n.settle(peer); err != nil {
		return errors.WithMessage(err, "settling")
	}
	fmt.Printf("\r🏁 Settled channel with %s.\n", peer.alias)
	return nil
}

func (n *node) settle(p *peer) error {
	p.ch.log.Debug("Settling")
	ctx, cancel := context.WithTimeout(context.Background(), config.Channel.SettleTimeout)
	defer cancel()

	if err := p.ch.Settle(ctx, false); err != nil {
		return errors.WithMessage(err, "settling the channel")
	}

	if err := p.ch.Close(); err != nil {
		return errors.WithMessage(err, "channel closing")
	}
	p.ch.log.Debug("Removing channel")
	p.ch = nil
	return nil
}

// Info prints the phase of all channels.
func (n *node) Info(args []string) error {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	n.log.Traceln("Info...")

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(config.Chain.TxTimeoutSec)*time.Second)
	defer cancel()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', tabwriter.Debug)
	fmt.Fprintf(w, "Peer\tPhase\tVersion\tMy D\tPeer D\tMy On-Chain D\tPeer On-Chain D\t\n")
	for alias, peer := range n.peers {
		onChainBals, err := n.getOnChainBal(ctx, n.onChain.Address(), peer.perunID)
		if err != nil {
			return err
		}
		onChainBalsEth := dotclient.NewDotsFromPlanks(onChainBals...)
		if peer.ch == nil {
			fmt.Fprintf(w, "%s\t%s\t \t \t \t%v\t%v\t\n", alias, "Connected", onChainBalsEth[0], onChainBalsEth[1])
		} else {
			bals := dotclient.NewDotsFromPlanks(peer.ch.GetBalances())
			fmt.Fprintf(w, "%s\t%v\t%v\t%v\t%v\t%v\t%v\t\n",
				alias, peer.ch.Phase(), peer.ch.State().Version, bals[0], bals[1], onChainBalsEth[0], onChainBalsEth[1])
		}
	}
	fmt.Fprintln(w)
	w.Flush()

	return nil
}

func (n *node) Exit([]string) error {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	n.log.Traceln("Exiting...")

	return n.client.Close()
}

func (n *node) ExistsPeer(alias string) bool {
	n.mtx.Lock()
	defer n.mtx.Unlock()

	return n.peers[alias] != nil
}
