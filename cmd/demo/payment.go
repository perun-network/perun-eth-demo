// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo

import (
	"context"
	"fmt"
	"math/big"

	"github.com/pkg/errors"
	"perun.network/go-perun/channel"
	"perun.network/go-perun/client"
	"perun.network/go-perun/log"
)

type (
	paymentChannel struct {
		*client.Channel

		log     log.Logger
		handler chan bool
		res     chan handlerRes
		onFinal func()
		// save the last state to circumvent the `channel.StateMtxd` problem
		lastState *channel.State
	}

	// A handlerRes encapsulates the result of a channel handling request
	handlerRes struct {
		up  client.ChannelUpdate
		err error
	}
)

func newPaymentChannel(ch *client.Channel) *paymentChannel {
	return &paymentChannel{
		Channel:   ch,
		log:       log.WithField("channel", ch.ID()),
		handler:   make(chan bool, 1),
		res:       make(chan handlerRes),
		lastState: ch.State(),
	}
}
func (ch *paymentChannel) sendMoney(amount *big.Int) error {
	return ch.sendUpdate(
		func(state *channel.State) error {
			transferBal(stateBals(state), ch.Idx(), amount)
			return nil
		}, "sendMoney")
}

func (ch *paymentChannel) sendFinal() error {
	ch.log.Debugf("Sending final state")
	return ch.sendUpdate(func(state *channel.State) error {
		state.IsFinal = true
		return nil
	}, "final")
}

func (ch *paymentChannel) sendUpdate(update func(*channel.State) error, desc string) error {
	ch.log.Debugf("Sending update: %s", desc)
	ctx, cancel := context.WithTimeout(context.Background(), config.Channel.Timeout)
	defer cancel()

	stateBefore := ch.State()
	err := ch.UpdateBy(ctx, update)
	ch.log.Debugf("Sent update: %s, err: %v", desc, err)

	state := ch.State()
	balChanged := stateBefore.Balances[0][0].Cmp(state.Balances[0][0]) != 0
	if balChanged {
		bals := weiToEther(state.Allocation.Balances[0]...)
		fmt.Printf("💰 Sent payment. New balance: [My: %v Ξ, Peer: %v Ξ]\n", bals[ch.Idx()], bals[1-ch.Idx()]) // assumes two-party channel
	}
	if err == nil {
		ch.lastState = state
	}

	return err
}

func transferBal(bals []channel.Bal, ourIdx channel.Index, amount *big.Int) {
	a := new(big.Int).Set(amount) // local copy because we mutate it
	otherIdx := ourIdx ^ 1
	ourBal := bals[ourIdx]
	otherBal := bals[otherIdx]
	otherBal.Add(otherBal, a)
	ourBal.Sub(ourBal, a)
}

func stateBals(state *channel.State) []channel.Bal {
	return state.Balances[0]
}

func (ch *paymentChannel) Handle(update client.ChannelUpdate, res *client.UpdateResponder) {
	oldBal := stateBals(ch.lastState)
	balChanged := oldBal[0].Cmp(update.State.Balances[0][0]) != 0
	ctx, cancel := context.WithTimeout(context.Background(), config.Channel.Timeout)
	defer cancel()
	if err := assertValidTransition(ch.lastState, update.State, update.ActorIdx); err != nil {
		res.Reject(ctx, "invalid transition")
	} else if err := res.Accept(ctx); err != nil {
		ch.log.Error(errors.WithMessage(err, "handling payment update"))
	}

	if balChanged {
		bals := weiToEther(update.State.Allocation.Balances[0]...)
		PrintfAsync("💰 Received payment. New balance: [My: %v Ξ, Peer: %v Ξ]\n", bals[ch.Idx()], bals[1-ch.Idx()])
	}
	ch.lastState = update.State.Clone()
}

// assertValidTransition checks that money flows only from the actor to the
// other participants.
func assertValidTransition(from, to *channel.State, actor channel.Index) error {
	if !channel.IsNoData(to.Data) {
		return errors.New("channel must not have app data")
	}
	for i, asset := range from.Balances {
		for j, bal := range asset {
			if int(actor) == j && bal.Cmp(to.Balances[i][j]) == -1 {
				return errors.Errorf("payer[%d] steals asset %d, so %d < %d", j, i, bal, to.Balances[i][j])
			} else if int(actor) != j && bal.Cmp(to.Balances[i][j]) == 1 {
				return errors.Errorf("payer[%d] reduces participant[%d]'s asset %d", actor, j, i)
			}
		}
	}
	return nil
}

func (ch *paymentChannel) GetBalances() (our, other *big.Int) {
	bals := stateBals(ch.State())
	if len(bals) != 2 {
		return new(big.Int), new(big.Int)
	}
	return bals[ch.Idx()], bals[1-ch.Idx()]
}
