// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo

import (
	"bufio"
	"context"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/pkg/errors"

	"perun.network/go-perun/apps/payment"
	_ "perun.network/go-perun/backend/ethereum" // backend init
	echannel "perun.network/go-perun/backend/ethereum/channel"
	ewallet "perun.network/go-perun/backend/ethereum/wallet"
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

	// Account for signing on-chain TX. Currently also the Perun-ID.
	onChain wallet.Account
	// Account for signing off-chain TX. Currently one Account for all
	// state channels, later one we want one Account per Channel.
	offChain wallet.Account
	wallet   *ewallet.Wallet

	adjudicator channel.Adjudicator
	adjAddr     common.Address
	asset       channel.Asset
	assetAddr   common.Address
	funder      channel.Funder
	// Needed to deploy contracts.
	cb echannel.ContractBackend

	// Protects peers
	mtx   sync.Mutex
	peers map[string]*peer
}

func getOnChainBal(ctx context.Context, addrs ...wallet.Address) ([]*big.Int, error) {
	bals := make([]*big.Int, len(addrs))
	var err error
	for idx, addr := range addrs {
		bals[idx], err = ethereumBackend.BalanceAt(ctx, ewallet.AsEthAddr(addr), nil)
		if err != nil {
			return nil, errors.Wrap(err, "querying on-chain balance")
		}
	}
	return bals, nil
}

func (n *node) Connect(args []string) error {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	n.log.Traceln("Connecting...")
	alias := args[0]

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

	// This also starts the watcher.
	p.ch = newPaymentChannel(ch, func() {
		n.handleFinal(p)
	})

	bals := weiToEther(ch.State().Balances[0]...)
	fmt.Printf("\n🆕 Channel established with %s. Initial balance: [My: %v Ξ, Peer: %v Ξ]\n",
		p.alias, bals[ch.Idx()], bals[1-ch.Idx()]) // assumes two-party channel
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

func (n *node) HandleUpdate(update client.ChannelUpdate, resp *client.UpdateResponder) {
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

// HandleProposal is called when a new channel is proposed
func (n *node) HandleProposal(req *client.ChannelProposal, res *client.ProposalResponder) {
	if len(req.PeerAddrs) != 2 {
		log.Fatal("Only channels with two participants are currently supported")
	}

	n.mtx.Lock()
	defer n.mtx.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), config.Node.HandleTimeout)
	defer cancel()
	id := req.PeerAddrs[0]
	n.log.Debug("Received channel propsal")

	// Find the peer by its perunID and create it if not present
	p := n.peer(id)
	alias, cfg := findConfig(id)

	if p == nil {
		if cfg == nil {
			res.Reject(ctx, "Unknown identity")
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
	n.log.WithField("peer", id).Debug("Channel propsal")

	// TODO: implement print balance with support for arbitrary number of participants
	fmt.Printf("\n💭 Received channel proposal from %v with funding %v.\n", alias, weiToEther(req.InitBals.Balances[0]...))
	fmt.Printf("❓ Enter \"accept\" to accept, \"reject\" to reject:\n")

	// TODO: use prompt for input once available in package
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		n.log.Error("reading user input")
		return
	}
	userInput := scanner.Text()
	if userInput == "accept" {
		if _, err := res.Accept(ctx, client.ProposalAcc{
			Participant: n.offChain.Address(),
		}); err != nil {
			n.log.Error(errors.WithMessage(err, "accepting channel proposal"))
			return
		}

		fmt.Println("✅ Channel proposal accepted")
	} else {
		if err := res.Reject(ctx, "rejected by user"); err != nil {
			n.log.Error(errors.WithMessage(err, "rejecting channel proposal"))
			return
		}

		fmt.Println("❌ Channel proposal rejected")
	}
}

// handleFinal is called when the channel with peer `p` received a final update,
// indicating closure.
func (n *node) handleFinal(p *peer) {
	// Without on-chain watchers we just wait one second before try to settle.
	// Otherwise out settling could collide with the other party's.
	// Needs to be increased for geth-nodes but works for ganache.
	time.Sleep(time.Second)
	n.mtx.Lock()
	defer n.mtx.Unlock()
	n.settle(p)
}

func (n *node) Open(args []string) error {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	peer := n.peers[args[0]]
	if peer == nil {
		return errors.Errorf("peer not found %s", args[0])
	}
	myBalEth, _ := new(big.Float).SetString(args[1]) // Input was already validated by command parser.
	peerBalEth, _ := new(big.Float).SetString(args[2])

	initBals := &channel.Allocation{
		Assets:   []channel.Asset{n.asset},
		Balances: [][]*big.Int{etherToWei(myBalEth, peerBalEth)},
	}
	prop := &client.ChannelProposal{
		ChallengeDuration: config.Channel.ChallengeDurationSec,
		Nonce:             nonce(),
		ParticipantAddr:   n.offChain.Address(),
		AppDef:            payment.AppDef(),
		InitData:          new(payment.NoData),
		InitBals:          initBals,
		PeerAddrs:         []wallet.Address{n.onChain.Address(), peer.perunID},
	}

	alias := args[0]
	fmt.Printf("💭 Proposing channel to %v...\n", alias)

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

	return peer.ch.sendMoney(etherToWei(amountEth)[0])
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

	return n.settle(peer)
}

func (n *node) settle(p *peer) error {
	p.ch.log.Debug("Settling")
	ctx, cancel := context.WithTimeout(context.Background(), config.Channel.SettleTimeout)
	defer cancel()
	finalBals := weiToEther(p.ch.GetBalances())
	if err := p.ch.Settle(ctx); err != nil {
		return errors.WithMessage(err, "settling the channel")
	}

	if err := p.ch.Close(); err != nil {
		return errors.WithMessage(err, "channel closing")
	}
	p.ch.log.Debug("Removing channel")
	p.ch = nil
	fmt.Printf("\n🏁 Settled channel with %s. Final Balance: [My: %v Ξ, Peer: %v Ξ]\n", p.alias, finalBals[0], finalBals[1])

	return nil
}

// Info prints the phase of all channels.
func (n *node) Info(args []string) error {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	n.log.Traceln("Info...")

	ctx, cancel := context.WithTimeout(context.Background(), config.Chain.TxTimeout)
	defer cancel()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', tabwriter.Debug)
	fmt.Fprintf(w, "Peer\tPhase\tVersion\tMy Ξ\tPeer Ξ\tMy On-Chain Ξ\tPeer On-Chain Ξ\t\n")
	for alias, peer := range n.peers {
		onChainBals, err := getOnChainBal(ctx, n.onChain.Address(), peer.perunID)
		if err != nil {
			return err
		}
		onChainBalsEth := weiToEther(onChainBals...)
		if peer.ch == nil {
			fmt.Fprintf(w, "%s\t%s\t \t \t \t%v\t%v\t\n", alias, "Connected", onChainBalsEth[0], onChainBalsEth[1])
		} else {
			bals := weiToEther(peer.ch.GetBalances())
			fmt.Fprintf(w, "%s\t%v\t%d\t%v\t%v\t%v\t%v\t\n",
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
