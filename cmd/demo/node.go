// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo

import (
	"context"
	"fmt"
	"math/big"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/pkg/errors"

	"perun.network/go-perun/apps/payment"
	_ "perun.network/go-perun/backend/ethereum" // backend init
	echannel "perun.network/go-perun/backend/ethereum/channel"
	ewallet "perun.network/go-perun/backend/ethereum/wallet"
	"perun.network/go-perun/channel"
	"perun.network/go-perun/client"
	"perun.network/go-perun/log"
	"perun.network/go-perun/peer/net"
	"perun.network/go-perun/wallet"
	wtest "perun.network/go-perun/wallet/test"
)

type peer struct {
	alias   string
	perunID wallet.Address
	ch      *paymentChannel
}

type node struct {
	log log.Logger

	client *client.Client
	dialer *net.Dialer

	// Account for signing on-chain TX. Currently also the Perun-ID.
	onChain wallet.Account
	// Account for signing off-chain TX. Currently one Account for all
	// state channels, later one we want one Account per Channel.
	offChain wallet.Account

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

var backend *node

func newNode() (*node, error) {
	wallet, acc, err := importAccount(config.SecretKey)
	if err != nil {
		return nil, errors.WithMessage(err, "importing secret key")
	}

	dialer := net.NewTCPDialer(config.Node.DialTimeout)
	backend, err := ethclient.Dial(config.Chain.URL)
	if err != nil {
		return nil, errors.WithMessage(err, "connecting to ethereum node")
	}

	n := &node{
		log:     log.Get(),
		onChain: acc,
		dialer:  dialer,
		cb:      echannel.NewContractBackend(backend, wallet.Ks, &acc.Account),
		peers:   make(map[string]*peer),
	}

	if err := n.setupContracts(); err != nil {
		return nil, errors.WithMessage(err, "setting up contracts")
	}
	return n, n.listen()
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
	ctx, cancel := context.WithTimeout(context.Background(), config.Node.DialTimeout)
	defer cancel()
	if _, err := n.dialer.Dial(ctx, peerCfg.perunID); err != nil {
		return errors.WithMessage(err, "could not connect to peer")
	}
	n.peers[alias] = &peer{
		alias:   alias,
		perunID: peerCfg.perunID,
	}

	return nil
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

// deployAdjudicator deploys the Adjudicator to the blockchain and returns its address
// or an error.
func deployAdjudicator(cb echannel.ContractBackend) (common.Address, error) {
	ctx, cancel := context.WithTimeout(context.Background(), config.Chain.DeployTimeout)
	defer cancel()
	adjAddr, err := echannel.DeployAdjudicator(ctx, cb)
	return adjAddr, errors.WithMessage(err, "deploying eth adjudicator")
}

// deployAsset deploys the Assetholder to the blockchain and returns its address
// or an error. Needs an Adjudicator address as second argument.
func deployAsset(cb echannel.ContractBackend, adjudicator common.Address) (common.Address, error) {
	ctx, cancel := context.WithTimeout(context.Background(), config.Chain.DeployTimeout)
	defer cancel()
	asset, err := echannel.DeployETHAssetholder(ctx, cb, adjudicator)
	return asset, errors.WithMessage(err, "deploying eth assetholder")
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
	} else {
		tmpAdj, err := strToAddress(config.Chain.Adjudicator)
		if err != nil {
			return err
		}
		adjAddr = ewallet.AsEthAddr(tmpAdj)
	}

	if config.Chain.Assetholder == "deploy" {
		assAddr, err = deployAsset(n.cb, adjAddr)
		if err != nil {
			return
		}
	} else {
		tmpAsset, err := strToAddress(config.Chain.Assetholder)
		if err != nil {
			return err
		}
		assAddr = ewallet.AsEthAddr(tmpAsset)
	}
	n.adjAddr = adjAddr
	n.assetAddr = assAddr

	var recvAddr common.Address
	recvAddr.SetBytes(n.onChain.Address().Bytes())

	n.adjudicator = echannel.NewAdjudicator(n.cb, adjAddr, recvAddr)
	n.funder = echannel.NewETHFunder(n.cb, assAddr)
	n.asset = (*ewallet.Address)(&assAddr)
	n.log.WithField("Adj", adjAddr).WithField("Asset", assAddr).Debug("Set contracts")
	return
}

// listen listen for incoming TCP connections and print the configuration, since this is
// the last step of the startup for a node.
func (n *node) listen() error {
	// here we simulate the generation of a new account from a wallet
	rng := rand.New(rand.NewSource(int64(time.Now().UnixNano()))) // <- not secure
	n.offChain = wtest.NewRandomAccount(rng)
	n.log.WithField("off-chain", n.offChain.Address()).Info("Generating account")

	n.client = client.New(n.onChain, n.dialer, n.funder, n.adjudicator)
	host := config.Node.IP + ":" + strconv.Itoa(int(config.Node.Port))
	n.log.WithField("host", host).Trace("Listening for connections")
	listener, err := net.NewTCPListener(host)
	if err != nil {
		return errors.WithMessage(err, "could not start tcp listener")
	}
	go n.client.HandleChannelProposals(n)
	go n.client.Listen(listener)
	n.PrintConfig()
	return nil
}

// getPeer returns the peer with the address `addr` or nil if not found.
func (n *node) getPeer(addr wallet.Address) *peer {
	for _, peer := range n.peers {
		if peer.perunID == addr {
			return peer
		}
	}
	return nil
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

var aliasCounter int

func findConfig(id wallet.Address) (string, *netConfigEntry) {
	for alias, e := range config.Peers {
		if e.perunID.String() == id.String() {
			return alias, e
		}
	}
	return "", nil
}

func (n *node) Handle(req *client.ChannelProposalReq, res *client.ProposalResponder) {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), config.Node.HandleTimeout)
	defer cancel()
	id := req.PeerAddrs[0]
	n.log.Debug("Received channel propsal")

	// Find the peer by its perunID and create it if not present
	p := n.getPeer(id)
	alias, cfg := findConfig(id)

	if p == nil {
		if cfg == nil {
			res.Reject(ctx, "Unknown identity")
			return
		}
		p = &peer{
			alias:   alias,
			perunID: id,
		}
		n.peers[alias] = p
		n.log.WithField("id", id).WithField("alias", alias).Debug("New peer")
	}
	n.log.WithField("from", id).Debug("Channel propsal")

	_ch, err := res.Accept(ctx, client.ProposalAcc{
		Participant: n.offChain,
	})
	if err != nil {
		n.log.Error(errors.WithMessage(err, "accepting channel proposal"))
		return
	}

	// Add the channel to the peer and start listening for updates
	p.ch = newPaymentChannel(_ch, func() {
		n.handleFinal(p)
	})
	go p.ch.ListenUpdates()
	fmt.Println("\n🆕 Channel opened with", alias, " initial balance:", req.InitBals.Balances[0])
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
	myBals, _ := strconv.ParseInt(args[1], 10, 32) // error already checked by validator
	peerBals, _ := strconv.ParseInt(args[2], 10, 32)

	initBals := &channel.Allocation{
		Assets: []channel.Asset{n.asset},
		Balances: [][]*big.Int{
			{big.NewInt(myBals),
				big.NewInt(peerBals)},
		},
	}
	prop := &client.ChannelProposal{
		ChallengeDuration: config.Channel.ChallengeDurationSec,
		Nonce:             nonce(),
		Account:           n.offChain,
		AppDef:            payment.AppDef(),
		InitData:          new(payment.NoData),
		InitBals:          initBals,
		PeerAddrs:         []wallet.Address{n.onChain.Address(), peer.perunID},
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.Channel.FundTimeout)
	defer cancel()
	n.log.Debug("Proposing channel")
	_ch, err := n.client.ProposeChannel(ctx, prop)
	if err != nil {
		return errors.WithMessage(err, "proposing channel failed")
	}
	n.log.Debug("Proposing done")

	peer.ch = newPaymentChannel(_ch, func() {
		n.handleFinal(peer)
	})
	go peer.ch.ListenUpdates()

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
	wei, _ := strconv.ParseInt(args[1], 10, 32)
	return peer.ch.sendMoney(big.NewInt(wei))
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
	if err := p.ch.Settle(ctx); err != nil {
		return errors.WithMessage(err, "settling the channel")
	}

	if err := p.ch.Close(); err != nil {
		return errors.WithMessage(err, "channel closing")
	}
	p.ch.log.Debug("Removing channel")
	p.ch = nil
	fmt.Println("🏁 Settled channel with", p.alias)

	return nil
}

// Info prints the phase of all channels.
func (n *node) Info(args []string) error {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	n.log.Traceln("Info...")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', tabwriter.Debug)
	fmt.Fprintf(w, "Peer\tPhase\tVersion\tMy Ξ\tPeer Ξ\t\n")
	for alias, peer := range n.peers {
		if peer.ch == nil {
			fmt.Fprintf(w, "%s\t%s\t\n", alias, "Connected")
		} else {
			my, other := peer.ch.GetBalances()
			fmt.Fprintf(w, "%s\t%v\t%d\t%v\t%v\t\n",
				alias, peer.ch.Phase(), peer.ch.State().Version, my, other)
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

// Setup initializes the node, can not be done init() since it needs the configuration
// from viper.
func Setup() {
	SetConfig()
	rng := rand.New(rand.NewSource(0x280a0f350eec))
	appDef := wtest.NewRandomAddress(rng)
	payment.SetAppDef(appDef)

	b, err := newNode()
	if err != nil {
		log.Fatalln("init error:", err)
	}
	backend = b
}
