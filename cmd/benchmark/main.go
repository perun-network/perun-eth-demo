package benchmark

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	hdwallet "github.com/miguelmota/go-ethereum-hdwallet"
	"github.com/pkg/errors"
	"github.com/spf13/cobra"
	echannel "perun.network/go-perun/backend/ethereum/channel"
	"perun.network/go-perun/backend/ethereum/wallet"
	"perun.network/go-perun/backend/ethereum/wallet/hd"
	"perun.network/go-perun/channel"
	"perun.network/go-perun/client"
	"perun.network/go-perun/wire"
)

var cmdFlags struct {
	Network  string
	Mnemonic string
}

func GetCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "benchmark",
		Short: "Benchmark gas usage of L1 vs Rollups",
		Long:  "Benchmark gas usage of L1 vs Arbitrum Rollup vs Optimism Rollup",
		Run:   run,
	}

	cmd.Flags().StringVar(&cmdFlags.Network, "network", "ganache", "The blockchain network. One of [ganache, optimism, arbitrum].")
	cmd.Flags().StringVar(&cmdFlags.Mnemonic, "mnenomic", "pistol kiwi shrug future ozone ostrich match remove crucial oblige cream critic", "The mnemonic from which accounts are derived.")

	return cmd
}

const (
	commandTimeout    = 60 * time.Second
	challengeDuration = 60
)

type networkConfig struct {
	nodeURL1    string
	nodeURL2    string
	chainID     int64
	gasLimit    uint64
	adjudicator common.Address
	asset       common.Address
	assetHolder common.Address
}

var configs = map[string]networkConfig{
	"ganache": {
		nodeURL1:    "ws://127.0.0.1:8545",
		nodeURL2:    "ws://127.0.0.1:8545",
		chainID:     1337,
		gasLimit:    6600000,
		adjudicator: common.HexToAddress("0x2411fA9EabdF4Bd589678dcF461613af63296A5d"),
		asset:       common.HexToAddress("0x213ac92B798C9D4e971bd7363fE826538F1a70AC"),
		assetHolder: common.HexToAddress("0xbB7e3b2D5286153a46648639B56384476699E4D4"),
	},
	"arbitrum_local": {
		nodeURL1:    "ws://127.0.0.1:7546",
		nodeURL2:    "ws://127.0.0.1:8548",
		chainID:     0x8bf17f3ea3a3,
		gasLimit:    66000000,
		adjudicator: common.HexToAddress("0x2411fA9EabdF4Bd589678dcF461613af63296A5d"),
		asset:       common.HexToAddress("0x213ac92B798C9D4e971bd7363fE826538F1a70AC"),
		assetHolder: common.HexToAddress("0xbB7e3b2D5286153a46648639B56384476699E4D4"),
	},
	"optimism_local": {
		nodeURL1:    "ws://127.0.0.1:9545",
		nodeURL2:    "ws://127.0.0.1:8546",
		chainID:     420,
		gasLimit:    8999999,
		adjudicator: common.HexToAddress("0x2411fA9EabdF4Bd589678dcF461613af63296A5d"),
		asset:       common.HexToAddress("0x213ac92B798C9D4e971bd7363fE826538F1a70AC"),
		assetHolder: common.HexToAddress("0xbB7e3b2D5286153a46648639B56384476699E4D4"),
	},
}

func init() {
	echannel.SetGasPrice(0)
	echannel.GasLimit = 6600000
}

func run(cmd *cobra.Command, args []string) {
	Execute(cmd.Context(), cmdFlags.Network, cmdFlags.Mnemonic)
}

func Execute(ctx context.Context, network string, mnemonic string) {
	cfg, ok := configs[network]
	if !ok {
		panic("invalid network")
	}

	nodeURL1 := cfg.nodeURL1
	nodeURL2 := cfg.nodeURL2
	chainID := cfg.chainID
	assetHolder := cfg.assetHolder
	asset := cfg.asset
	adjudicator := cfg.adjudicator

	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	b := getCurrentBlock(nodeURL1)

	bus := wire.NewLocalBus()

	c1, err := setupClient(
		ctx,
		mnemonic,
		0,
		nodeURL2,
		chainID,
		assetHolder,
		asset,
		adjudicator,
		bus,
	)
	if err != nil {
		panic(err)
	}

	c2, err := setupClient(
		ctx,
		mnemonic,
		1,
		nodeURL2,
		chainID,
		assetHolder,
		asset,
		adjudicator,
		bus,
	)
	if err != nil {
		panic(err)
	}

	// Setup proposal handler for Client2.
	var ch2 *client.Channel
	proposalHandler := &FunctionProposalHandler{
		openingProposalHandler: func(cp client.ChannelProposal, pr *client.ProposalResponder) {
			switch cp := cp.(type) {
			case *client.LedgerChannelProposal:
				_ch, err := pr.Accept(ctx, cp.Accept(c2.Address(), client.WithRandomNonce()))
				if err != nil {
					panic(err)
				}
				ch2 = _ch
			}
		},
		updateProposalHandler: func(s *channel.State, cu client.ChannelUpdate, ur *client.UpdateResponder) {
			ur.Accept(ctx)
		},
	}
	go c2.Handle(proposalHandler, proposalHandler)

	// Client1 proposes channel to Client2
	prop, err := client.NewLedgerChannelProposal(
		challengeDuration,
		c1.Address(),
		&channel.Allocation{
			Assets:   []channel.Asset{wallet.AsWalletAddr(assetHolder)},
			Balances: [][]*big.Int{{big.NewInt(1), big.NewInt(1)}},
		},
		[]wire.Address{c1.Address(), c2.Address()},
		client.WithRandomNonce(),
	)
	if err != nil {
		panic(err)
	}
	ch1, err := c1.ProposeChannel(ctx, prop)
	if err != nil {
		panic(err)
	}

	err = ch1.UpdateBy(ctx, func(s *channel.State) error {
		diff := big.NewInt(1)
		s.Allocation.Balances[0][0].Sub(s.Allocation.Balances[0][0], diff)
		s.Allocation.Balances[0][1].Add(s.Allocation.Balances[0][1], diff)
		s.IsFinal = true
		return nil
	})
	if err != nil {
		panic(err)
	}

	err = ch1.Register(ctx)
	if err != nil {
		panic(err)
	}

	err = ch1.Settle(ctx, false)
	if err != nil {
		panic(err)
	}

	err = ch2.Register(ctx)
	if err != nil {
		panic(err)
	}

	err = ch2.Settle(ctx, false)
	if err != nil {
		panic(err)
	}

	printGasUsageFromBlock(b, nodeURL1)
}

type FunctionProposalHandler struct {
	openingProposalHandler client.ProposalHandlerFunc
	updateProposalHandler  client.UpdateHandlerFunc
}

func (h *FunctionProposalHandler) HandleProposal(p client.ChannelProposal, r *client.ProposalResponder) {
	h.openingProposalHandler(p, r)
}

func (h *FunctionProposalHandler) HandleUpdate(prev *channel.State, next client.ChannelUpdate, r *client.UpdateResponder) {
	h.updateProposalHandler(prev, next, r)
}

type Client struct {
	*hd.Account
	*client.Client
}

func setupClient(
	ctx context.Context,
	mnemonic string,
	accountIndex uint,
	nodeURL string,
	chainID int64,
	assetHolder common.Address,
	asset common.Address,
	adjudicator common.Address,
	bus wire.Bus,
) (c *Client, err error) {
	w, acc, err := setupWallet(mnemonic, accountIndex)
	if err != nil {
		return
	}

	cb, err := setupContractBackend(ctx, nodeURL, w, chainID)
	if err != nil {
		return
	}
	funder := setupFunder(cb, acc, assetHolder, asset)

	adj := echannel.NewAdjudicator(cb, adjudicator, acc.Account.Address, acc.Account)

	_c, err := client.New(
		acc.Address(),
		bus,
		funder,
		adj,
		w,
	)
	if err != nil {
		return
	}

	c = &Client{
		Account: acc,
		Client:  _c,
	}
	return
}

func setupContractBackend(ctx context.Context, nodeURL string, w *hd.Wallet, chainID int64) (cb echannel.ContractBackend, err error) {
	ethClient, err := ethclient.DialContext(ctx, nodeURL)
	if err != nil {
		return
	}

	signer := types.NewEIP155Signer(big.NewInt(chainID))
	cb = echannel.NewContractBackend(ethClient, hd.NewTransactor(w.Wallet(), signer))
	return
}

func setupFunder(cb echannel.ContractBackend, acc *hd.Account, assetHolder common.Address, asset common.Address) channel.Funder {
	accounts := make(map[echannel.Asset]accounts.Account)
	accounts[wallet.Address(assetHolder)] = acc.Account

	depositors := make(map[echannel.Asset]echannel.Depositor)
	depositors[wallet.Address(assetHolder)] = &echannel.ERC20Depositor{Token: asset}

	return echannel.NewFunder(cb, accounts, depositors)
}

func setupWallet(mnemonic string, accountIndex uint) (*hd.Wallet, *hd.Account, error) {
	wallet, err := hdwallet.NewFromMnemonic(mnemonic)
	if err != nil {
		return nil, nil, errors.WithMessage(err, "creating hdwallet")
	}

	perunWallet, err := hd.NewWallet(wallet, accounts.DefaultBaseDerivationPath.String(), accountIndex)
	if err != nil {
		return nil, nil, errors.WithMessage(err, "creating perun wallet")
	}
	acc, err := perunWallet.NewAccount()
	if err != nil {
		return nil, nil, errors.WithMessage(err, "creating account")
	}

	return perunWallet, acc, nil
}

func getCurrentBlock(ethURL string) uint64 {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := ethclient.DialContext(ctx, ethURL)
	if err != nil {
		panic(err)
	}

	n, err := client.BlockNumber(ctx)
	if err != nil {
		panic(err)
	}
	return n
}

func printGasUsageFromBlock(b uint64, ethURL string) error {
	for b == getCurrentBlock(ethURL) {
		fmt.Println("waiting for block")
		time.Sleep(1 * time.Second)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := ethclient.DialContext(ctx, ethURL)
	if err != nil {
		return err
	}

	n, err := client.BlockNumber(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("\n### L1 Gas Usage ###\n\n")

	gasUsedAccumulated := 0
	for i := n; i > b; i-- {
		b, err := client.BlockByNumber(ctx, big.NewInt(int64(i)))
		if err != nil {
			return err
		}
		fmt.Printf("Block %v: %v Gas\n", b.Hash(), b.GasUsed())
		gasUsedAccumulated += int(b.GasUsed())
	}

	fmt.Printf("\nTotal: %v Gas\n\n", gasUsedAccumulated)

	return nil
}
