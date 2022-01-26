/**
 * Copyright (C) 2021 The poly network Authors
 * This file is part of The poly network library.
 *
 * The poly network is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Lesser General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * The poly network is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Lesser General Public License for more details.
 *
 * You should have received a copy of the GNU Lesser General Public License
 * along with the poly network.  If not, see <http://www.gnu.org/licenses/>.
 *
 */
package voter

import (
	"context"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/ontio/ontology/smartcontract/service/native/cross_chain/cross_chain_manager"
	"github.com/polynetwork/flow-voter/pkg/log"
	"github.com/polynetwork/poly-go-sdk/common"
	"github.com/polynetwork/poly/core/types"

	"github.com/onflow/cadence/runtime"
	"github.com/onflow/flow-go-sdk/client"
	"github.com/onflow/flow-go-sdk/crypto"
	"github.com/onflow/flow-go/crypto/hash"
	gocrypto "github.com/onflow/flow-go/fvm/crypto"
	"github.com/polynetwork/flow-voter/config"
	"github.com/polynetwork/flow-voter/pkg/db"
	sdk "github.com/polynetwork/poly-go-sdk"
	common1 "github.com/polynetwork/poly/common"
	common2 "github.com/polynetwork/poly/native/service/cross_chain_manager/common"
	autils "github.com/polynetwork/poly/native/service/utils"
	"github.com/zhiqiangxu/util"
	"google.golang.org/grpc"
)

type Voter struct {
	polySdk *sdk.PolySdk
	signer  *sdk.Account
	conf    *config.Config
	clients []*client.Client
	bdb     *db.BoltDB
	pk      crypto.PrivateKey
	hasher  hash.Hasher
	idx     int
}

func New(polySdk *sdk.PolySdk, signer *sdk.Account, conf *config.Config) *Voter {
	return &Voter{polySdk: polySdk, signer: signer, conf: conf}
}

func (v *Voter) init() (err error) {

	pkHex := hex.EncodeToString(polyPrivateKey2Hex(v.signer.PrivateKey))
	pk, err := crypto.DecodePrivateKeyHex(crypto.ECDSA_secp256k1, pkHex)
	if err != nil {
		return
	}
	tag := "FLOW-V0.0-user"
	hasher, err := gocrypto.NewPrefixedHashing(gocrypto.RuntimeToCryptoHashingAlgorithm(runtime.HashAlgorithmSHA2_256), tag)
	if err != nil {
		return
	}

	v.hasher = hasher
	v.pk = pk

	for _, url := range v.conf.FlowConfig.GrpcURL {
		var c *client.Client
		c, err = client.New(url, grpc.WithInsecure())
		if err != nil {
			return
		}
		v.clients = append(v.clients, c)
	}

	bdb, err := db.NewBoltDB(v.conf.BoltDbPath)
	if err != nil {
		return
	}

	v.bdb = bdb
	return
}

func (v *Voter) Start(ctx context.Context) {

	err := v.init()
	if err != nil {
		log.Fatalf("Voter.init failed:%v", err)
	}

	var wg sync.WaitGroup

	util.GoFunc(&wg, func() {
		v.monitorFlow(ctx)
	})
	util.GoFunc(&wg, func() {
		v.monitorPoly(ctx)
	})

	wg.Wait()
	return
}

func (v *Voter) getFlowStartHeight() (startHeight uint64) {
	startHeight = v.conf.ForceConfig.FlowHeight
	if startHeight > 0 {
		return
	}

	startHeight = v.bdb.GetFlowHeight()
	if startHeight > 0 {
		return
	}

	idx := randIdx(len(v.clients))
	c := v.clients[idx]
	header, err := c.GetLatestBlockHeader(context.Background(), true)
	if err != nil {
		log.Fatalf("GetLatestBlockHeader failed:%v", err)
	}
	startHeight = header.Height
	return
}

var FLOW_USEFUL_BLOCK_NUM = uint64(3)

func (v *Voter) monitorFlow(ctx context.Context) {

	nextHeight := v.getFlowStartHeight()

	ticker := time.NewTicker(time.Second * 2)

	for {
		select {
		case <-ticker.C:
			v.idx = randIdx(len(v.clients))
			c := v.clients[v.idx]
			header, err := c.GetLatestBlockHeader(context.Background(), true)
			if err != nil {
				log.Warnf("GetLatestBlockHeader failed:%v", err)
				sleep()
				continue
			}
			height := header.Height
			if height < nextHeight+FLOW_USEFUL_BLOCK_NUM {
				log.Infof("monitorFlow height(%d) < nextHeight(%d)+FLOW_USEFUL_BLOCK_NUM(%d)", height, nextHeight, FLOW_USEFUL_BLOCK_NUM)
				continue
			}

			for nextHeight < height-FLOW_USEFUL_BLOCK_NUM {
				select {
				case <-ctx.Done():
					log.Info("monitorFlow quiting from signal...")
					return
				default:
				}
				log.Infof("handling flow height:%d", nextHeight)
				err = v.fetchLockDepositEvents(nextHeight)
				if err != nil {
					log.Warnf("fetchLockDepositEvents failed:%v", err)
					sleep()
					v.idx = randIdx(len(v.clients))
					continue
				}
				nextHeight++
			}
			log.Infof("monitorFlow nextHeight:%d", nextHeight)
			err = v.bdb.UpdateFlowHeight(nextHeight)
			if err != nil {
				log.Warnf("UpdateFlowHeight failed:%v", err)
			}

		case <-ctx.Done():
			log.Info("monitorFlow quiting from signal...")
			return
		}
	}

}

func (v *Voter) fetchLockDepositEvents(height uint64) (err error) {

	c := v.clients[v.idx]
	blockEvents, err := c.GetEventsForHeightRange(context.Background(), client.EventRangeQuery{
		Type:        v.conf.FlowConfig.EventType,
		StartHeight: height,
		EndHeight:   height,
	})
	if err != nil {
		log.Warnf("GetEventsForHeightRange failed:%v", err)
		return
	}
	if len(blockEvents) == 0 {
		return
	}
	events := blockEvents[0].Events

	empty := true
	for _, event := range events {
		if event.Type != v.conf.FlowConfig.EventType {
			continue
		}
		fields := event.Value.Fields
		var rawParam []byte
		rawParam, err = hex.DecodeString(fields[len(fields)-1].ToGoValue().(string))
		if err != nil {
			log.Warnf("DecodeString rawParam failed:%v", err)
			return
		}

		param := &common2.MakeTxParam{}
		err = param.Deserialization(common1.NewZeroCopySource(rawParam))
		if err != nil {
			log.Warnf("DecodeString rawParam failed:%v", err)
			return
		}

		if !v.conf.IsWhitelistMethod(param.Method) {
			log.Warnf("target contract method invalid %s, height: %d", param.Method, height)
			continue
		}

		empty = false
		raw, _ := v.polySdk.GetStorage(autils.CrossChainManagerContractAddress.ToHexString(),
			append(append([]byte(cross_chain_manager.DONE_TX), autils.GetUint64Bytes(v.conf.FlowConfig.SideChainId)...), param.CrossChainID...))
		if len(raw) != 0 {
			log.Infof("fetchLockDepositEvents - ccid %s (tx_hash: %s) already on poly",
				hex.EncodeToString(param.CrossChainID), event.TransactionID.Hex())
			continue
		}

		var txHash string
		txHash, err = v.commitVote(uint32(height), rawParam, event.TransactionID.Bytes())
		if err != nil {
			log.Errorf("commitVote failed:%v", err)
			return
		}
		err = v.waitTx(txHash)
		if err != nil {
			log.Errorf("waitTx failed:%v txHash:%s", err, txHash)
			return
		}
	}

	log.Infof("flow height %d empty: %v", height, empty)
	return
}

func (v *Voter) waitTx(txHash string) (err error) {
	start := time.Now()
	var tx *types.Transaction
	for {
		tx, err = v.polySdk.GetTransaction(txHash)
		if tx == nil || err != nil {
			if time.Since(start) > time.Minute*5 {
				err = fmt.Errorf("waitTx timeout")
				return
			}
			time.Sleep(time.Second)
			continue
		}
		return
	}
}

func (v *Voter) commitVote(height uint32, value []byte, txhash []byte) (string, error) {
	log.Infof("commitVote, height: %d, value: %s, txhash: %s", height, hex.EncodeToString(value), hex.EncodeToString(txhash))
	tx, err := v.polySdk.Native.Ccm.ImportOuterTransfer(
		v.conf.FlowConfig.SideChainId,
		value,
		height,
		nil,
		v.signer.Address[:],
		[]byte{},
		v.signer)
	if err != nil {
		return "", err
	} else {
		log.Infof("commitVote - send transaction to poly chain: ( poly_txhash: %s, eth_txhash: %s, height: %d )",
			tx.ToHexString(), hex.EncodeToString(txhash), height)
		return tx.ToHexString(), nil
	}
}

var ONT_USEFUL_BLOCK_NUM = uint32(1)

func (v *Voter) getPolyStartHeight() (startHeight uint32) {
	startHeight = v.conf.ForceConfig.PolyHeight
	if startHeight > 0 {
		return
	}

	startHeight = v.bdb.GetPolyHeight()
	if startHeight > 0 {
		return
	}

	startHeight, err := v.polySdk.GetCurrentBlockHeight()
	if err != nil {
		log.Fatalf("polySdk.GetCurrentBlockHeight failed:%v", err)
	}
	return
}

func (v *Voter) monitorPoly(ctx context.Context) {

	ticker := time.NewTicker(time.Second)
	nextHeight := v.getPolyStartHeight()

	for {
		select {
		case <-ticker.C:
			height, err := v.polySdk.GetCurrentBlockHeight()
			if err != nil {
				log.Errorf("monitorPoly GetCurrentBlockHeight failed:%v", err)
				continue
			}
			height--
			if height < nextHeight+ONT_USEFUL_BLOCK_NUM {
				log.Infof("monitorPoly height(%d) < nextHeight(%d)+ONT_USEFUL_BLOCK_NUM(%d)", height, nextHeight, ONT_USEFUL_BLOCK_NUM)
				continue
			}

			for nextHeight < height-ONT_USEFUL_BLOCK_NUM {
				select {
				case <-ctx.Done():
					log.Info("monitorPoly quiting from signal...")
					return
				default:
				}
				log.Infof("handling poly height:%d", nextHeight)
				err = v.handleMakeTxEvents(nextHeight)
				if err != nil {
					log.Warnf("fetchLockDepositEvents failed:%v", err)
					sleep()
					continue
				}
				nextHeight++
			}
			log.Infof("monitorPoly nextHeight:%d", nextHeight)
			err = v.bdb.UpdatePolyHeight(nextHeight)
			if err != nil {
				log.Warnf("UpdateFlowHeight failed:%v", err)
			}
		case <-ctx.Done():
			log.Info("monitorPoly quiting from signal...")
			return
		}
	}
}

func (v *Voter) handleMakeTxEvents(height uint32) (err error) {

	hdr, err := v.polySdk.GetHeaderByHeight(height + 1)
	if err != nil {
		return
	}
	events, err := v.polySdk.GetSmartContractEventByBlock(height)
	if err != nil {
		return
	}

	empty := true

	for _, event := range events {
		for _, notify := range event.Notify {
			if notify.ContractAddress == v.conf.PolyConfig.EntranceContractAddress {
				states := notify.States.([]interface{})
				method, _ := states[0].(string)
				if method != "makeProof" {
					continue
				}

				if uint64(states[2].(float64)) != v.conf.FlowConfig.SideChainId {
					continue
				}
				empty = false
				var proof *common.MerkleProof
				proof, err = v.polySdk.GetCrossStatesProof(hdr.Height-1, states[5].(string))
				if err != nil {
					log.Errorf("handleMakeTxEvents - failed to get proof for key %s: %v", states[5].(string), err)
					return
				}
				auditpath, _ := hex.DecodeString(proof.AuditPath)
				value, _, _, _ := parseAuditpath(auditpath)
				param := &common2.ToMerkleValue{}
				if err = param.Deserialization(common1.NewZeroCopySource(value)); err != nil {
					log.Errorf("handleDepositEvents - failed to deserialize MakeTxParam (value: %x, err: %v)", value, err)
					return
				}

				var sig []byte
				sig, err = v.sign4Flow(value)
				if err != nil {
					log.Errorf("sign4Flow failed:%v", err)
					return
				}

				var txHash string
				txHash, err = v.commitSig(height, value, sig)
				if err != nil {
					log.Errorf("sign4Flow failed:%v", err)
					return
				}
				err = v.waitTx(txHash)
				if err != nil {
					log.Errorf("handleMakeTxEvents failed:%v", err)
					return
				}
			}
		}
	}

	log.Infof("poly height %d empty: %v", height, empty)
	return
}

func (v *Voter) sign4Flow(data []byte) (sig []byte, err error) {
	sig, err = v.pk.Sign([]byte(data), v.hasher)
	return
}

func (v *Voter) commitSig(height uint32, subject, sig []byte) (txHash string, err error) {

	hash, err := v.polySdk.Native.Sm.AddSignature(v.conf.FlowConfig.SideChainId, subject, sig, v.signer)
	if err != nil {
		return
	}

	txHash = hash.ToHexString()
	log.Infof("commitSig, height: %d, txhash: %s", height, txHash)
	return
}
