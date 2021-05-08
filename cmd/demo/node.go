// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"sync"
	"text/tabwriter"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/pkg/errors"

	_ "perun.network/go-perun/backend/ethereum" // backend init
	echannel "perun.network/go-perun/backend/ethereum/channel"
	ewallet "perun.network/go-perun/backend/ethereum/wallet"
	phd "perun.network/go-perun/backend/ethereum/wallet/hd"
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
	hub    *client.Hub

	// Account for signing on-chain TX. Currently also the Perun-ID.
	onChain *phd.Account
	// Account for signing off-chain TX. Currently one Account for all
	// state channels, later one we want one Account per Channel.
	offChain wallet.Account
	wallet   *phd.Wallet

	adjudicator channel.Adjudicator
	adjAddr     common.Address
	assets      map[string]*asset
	funder      channel.Funder
	// Needed to deploy contracts.
	cb echannel.ContractBackend

	// Protects peers
	mtx   sync.RWMutex
	peers map[string]*peer
}

func getOnChainBalETH(ctx context.Context, addrs ...wallet.Address) ([]*big.Int, error) {
	bals := make([]*big.Int, len(addrs))
	var err error
	for idx, addr := range addrs {
		bals[idx], err = ethereumBackend.BalanceAt(ctx, ewallet.AsEthAddr(addr), nil)
		if err != nil {
			return nil, errors.Wrap(err, "querying on-chain ETH balance")
		}
	}
	return bals, nil
}

func getOnChainBalERC20(ctx context.Context, token common.Address, addrs ...wallet.Address) ([]*big.Int, error) {
	erc20 := echannel.ERC20Token{CB: ethereumBackend, Address: token}
	bals := make([]*big.Int, len(addrs))

	var err error
	for idx, addr := range addrs {
		bals[idx], err = erc20.BalanceOf(ctx, ewallet.AsEthAddr(addr))
		if err != nil {
			return nil, errors.Wrap(err, "querying on-chain ERC20 balance")
		}
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

	perunID := ewallet.AsWalletAddr(peerCfg.PerunID)
	n.dialer.Register(perunID, peerCfg.Hostname+":"+strconv.Itoa(int(peerCfg.Port)))

	n.peers[alias] = &peer{
		alias:   alias,
		perunID: perunID,
		log:     log.WithField("peer", perunID),
	}

	fmt.Printf("📡 Connected to %v. Ready to open channel.\n", alias)

	return nil
}

// peer returns the peer with the address `addr` or creates it if not found.
func (n *node) peer(addr wire.Address) *peer {
	if addr == nil {
		n.log.Error("Nil address")
		return nil
	}
	for _, peer := range n.peers {
		if peer.perunID.Equals(addr) {
			return peer
		}
	}
	alias, cfg := findConfig(addr)

	if cfg == nil {
		return nil
	}
	p := &peer{
		alias:   alias,
		perunID: addr,
		log:     log.WithField("peer", addr),
	}
	n.peers[alias] = p
	n.log.WithField("channel", addr).WithField("alias", alias).Debug("New peer")

	return p
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

	bals := weiToEther(ch.State().Balances[0]...)
	assType, err := n.assetTypeFromState(*ch.State())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("🆕 Channel established with %[1]s. Initial balance: [My: %[2]v %[4]s, Peer: %[3]v %[4]s]\n",
		p.alias, bals[ch.Idx()], bals[1-ch.Idx()], assType.Symbol()) // assumes two-party channel
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
		if bytes.Equal(e.PerunID.Bytes(), id.Bytes()) {
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

func (n *node) handleLedgerProposal(prop *client.LedgerChannelProposal, asset asset, res *client.ProposalResponder) {
	id := prop.Peers[0]
	n.log.Debug("Received ledger channel proposal")
	// Find the peer by its perunID and create it if not present
	p := n.peer(id)
	if p == nil {
		n.rejectProposal(res, "Unknown identity")
	}
	n.log.WithField("peer", id).Debug("Channel proposal")

	bals := weiToEther(prop.InitBals.Balances[0]...)
	theirBal, ourBal := bals[0], bals[1] // proposer has index 0, receiver has index 1
	msg := fmt.Sprintf("🔁 Incoming channel proposal from %[1]v with funding [My: %[2]v %[4]s, Peer: %[3]v %[4]s].\nAccept (y/n)? ",
		alias, ourBal, theirBal, asset.Type.Symbol())
	Prompt(msg, func(userInput string) {
		ctx, cancel := context.WithTimeout(context.Background(), config.Node.HandleTimeout)
		defer cancel()

		if userInput == "y" {
			fmt.Printf("✅ Channel proposal accepted. Opening channel...\n")
			a := prop.Accept(n.offChain.Address(), client.WithRandomNonce())
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

func (n *node) handleVirtualProposal(prop *client.VirtualChannelProposal, asset asset, res *client.ProposalResponder) {
	id := prop.Peers[0]
	n.log.Debug("Received virtual channel proposal")

	p := n.peer(id)
	if p == nil {
		n.rejectProposal(res, "Unknown identity")
	}
	n.log.WithField("peer", id).Debug("Channel proposal")

	bals := weiToEther(prop.InitBals.Balances[0]...)
	theirBal, ourBal := bals[0], bals[1] // proposer has index 0, receiver has index 1
	msg := fmt.Sprintf("🔁 Incoming channel proposal from %[1]v with funding [My: %[2]v %[4]s, Peer: %[3]v %[4]s].\nAccept (y/n)? ",
		p.alias, ourBal, theirBal, asset.Type.Symbol())
	Prompt(msg, func(userInput string) {
		ctx, cancel := context.WithTimeout(context.Background(), config.Node.HandleTimeout)
		defer cancel()
		for {
			if userInput == "y" {
				fmt.Printf("✅ Virtual channel proposal accepted. Opening channel...\n")
				a := prop.Accept(n.offChain.Address(), client.WithRandomNonce())
				ch, err := res.Accept(ctx, a)
				if err != nil {
					n.log.Error(errors.WithMessage(err, "accepting channel proposal"))
					return
				}
				if n.channel(ch.ID()) == nil {
					n.log.Error("Could not set up channel")
					return
				}
				break
			} else {
				fmt.Printf("❌ Virtual channel proposal rejected\n")
				if err := res.Reject(ctx, "rejected by user"); err != nil {
					n.log.Error(errors.WithMessage(err, "rejecting channel proposal"))
					return
				}
				break
			}
		}
	})
}

func (n *node) HandleProposal(prop client.ChannelProposal, res *client.ProposalResponder) {
	if prop.Base().NumPeers() != 2 {
		log.Fatal("Only channels with two participants are currently supported.")
	}
	if len(prop.Base().InitBals.Assets) != 1 {
		log.Fatal("Only channels with one asset are currently supported.")
	}
	_assetAddr, ok := prop.Base().InitBals.Assets[0].(*echannel.Asset)
	if !ok {
		log.Fatal("Wrong asset type.")
	}
	asset, found := n.findAsset(common.Address(*_assetAddr))
	if !found {
		reason := fmt.Sprint("unknown asset")
		n.rejectProposal(res, reason)
		return
	}

	n.mtx.Lock()
	defer n.mtx.Unlock()
	switch prop := prop.(type) {
	case *client.LedgerChannelProposal:
		n.handleLedgerProposal(prop, *asset, res)
	case *client.VirtualChannelProposal:
		n.handleVirtualProposal(prop, *asset, res)
	default:
		log.Fatal("Unknown channel proposal type.")
	}
}

func (n *node) OpenVirtual(args []string) error {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	peer, err := n.ensurePeerConnected(args[0])
	if err != nil {
		return errors.WithMessage(err, "connecting to peer")
	}
	ingrid, err := n.ensurePeerConnected(args[1])
	if err != nil {
		return errors.WithMessage(err, "connecting to intermediary")
	}
	if peer.ch != nil {
		return errors.New("channel to peer already open")
	}
	if ingrid.ch == nil {
		return errors.New("no channel with intermediary")
	}

	asset := n.assets[args[3]]
	myBalEth, _ := new(big.Float).SetString(args[4]) // Input was already validated by command parser.
	peerBalEth, _ := new(big.Float).SetString(args[5])

	initBals := &channel.Allocation{
		Assets:   []channel.Asset{(*echannel.Asset)(&asset.Address)},
		Balances: [][]*big.Int{etherToWei(myBalEth, peerBalEth)},
	}

	otherChannel, err := hexutil.Decode(args[2])
	if err != nil {
		return errors.WithMessage(err, "parsing chanel id")
	}
	var id channel.ID
	copy(id[:], otherChannel)
	prop, err := client.NewVirtualChannelProposal(
		ingrid.ch.ID(),
		id,
		config.Channel.ChallengeDurationSec,
		n.offChain.Address(),
		initBals,
		[]wire.Address{n.onChain.Address(), peer.perunID},
		client.WithRandomNonce(),
	)
	if err != nil {
		return errors.WithMessage(err, "creating channel proposal")
	}

	fmt.Printf("💭 Proposing virtual channel to %v over %v...\n", args[0], args[1])

	ctx, cancel := context.WithTimeout(context.Background(), config.Channel.FundTimeout)
	defer cancel()
	n.log.Debug("Proposing virtual channel")
	ch, err := n.client.ProposeChannel(ctx, prop)
	if err != nil {
		return errors.WithMessage(err, "proposing virtual channel")
	}
	if n.channel(ch.ID()) == nil {
		return errors.New("OnNewChannel handler could not setup channel")
	}
	return nil
}

func (n *node) Open(args []string) error {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	peerName := args[0]
	peer, err := n.ensurePeerConnected(peerName)
	if err != nil {
		return errors.WithMessage(err, "connecting to peer")
	}

	asset := n.assets[args[1]]
	myBalEth, _ := new(big.Float).SetString(args[2]) // Input was already validated by command parser.
	peerBalEth, _ := new(big.Float).SetString(args[3])

	initBals := &channel.Allocation{
		Assets:   []channel.Asset{(*echannel.Asset)(&asset.Assetholder)},
		Balances: [][]*big.Int{etherToWei(myBalEth, peerBalEth)},
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
	fmt.Printf("✅ Opened channel 0x%x", ch.ID())
	return nil
}

func (n *node) rejectProposal(res *client.ProposalResponder, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), config.Node.HandleTimeout)
	defer cancel()
	if err := res.Reject(ctx, reason); err != nil {
		n.log.WithError(err).Warn("rejecting")
	}
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

	if err := p.ch.Register(ctx); err != nil {
		return errors.WithMessage(err, "registering")
	}
	if err := p.ch.Settle(ctx, p.ch.Idx() == 0); err != nil {
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

	asset := n.assets[args[0]]
	ctx, cancel := context.WithTimeout(context.Background(), config.Chain.TxTimeout)
	defer cancel()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', tabwriter.Debug)
	fmt.Fprintf(w, "Peer\tPhase\tVersion\tMy %[1]s\tPeer %[1]s\tMy On-Chain %[1]s\tPeer On-Chain %[1]s\t\n", asset.Type.Symbol())
	for alias, peer := range n.peers {
		var onChainBals []*big.Int
		var err error

		switch asset.Type {
		case assetTypeEth:
			onChainBals, err = getOnChainBalETH(ctx, n.onChain.Address(), peer.perunID)
		case assetTypeErc20:
			onChainBals, err = getOnChainBalERC20(ctx, asset.Address, n.onChain.Address(), peer.perunID)
		default:
			return errors.Errorf("Unknown asset type %v", asset.Type)
		}
		if err != nil {
			return err
		}
		onChainBalsEth := weiToEther(onChainBals...)
		if peer.ch == nil {
			fmt.Fprintf(w, "%s\t%s\t \t \t \t%v\t%v\t\n", alias, "Connected", onChainBalsEth[0], onChainBalsEth[1])
		} else {
			bals := []*big.Float{big.NewFloat(0), big.NewFloat(0)}
			assTypeCh, _ := n.assetTypeFromState(*peer.ch.lastState)
			if *assTypeCh == asset.Type {
				bals = weiToEther(peer.ch.GetBalances())
			}
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
	n.mtx.RLock()
	defer n.mtx.RUnlock()

	return n.peers[alias] != nil
}

func (n *node) ExistsAsset(asset string) bool {
	n.mtx.RLock()
	defer n.mtx.RUnlock()

	_, ok := n.assets[asset]
	return ok
}

func (n *node) findAsset(addr common.Address) (*asset, bool) {
	for _, asset := range n.assets {
		if bytes.Equal(asset.Assetholder.Bytes(), addr.Bytes()) {
			return asset, true
		}
	}
	return nil, false
}

func (n *node) assetTypeFromState(state channel.State) (*assetType, error) {
	assAddr := ewallet.AsEthAddr(state.Assets[0].(wallet.Address))
	asset, found := n.findAsset(assAddr)
	if !found {
		return nil, errors.Errorf("Could not find asset with address %v", assAddr)
	}
	return &asset.Type, nil
}

func (n *node) ensurePeerConnected(name string) (*peer, error) {
	peer := n.peers[name]
	if peer == nil {
		// try to connect to peer
		if err := n.connect(name); err != nil {
			return nil, err
		}
		peer = n.peers[name]
	}
	return peer, nil
}
