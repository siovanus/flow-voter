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
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	common2 "github.com/ethereum/go-ethereum/contracts/native/cross_chain_manager/common"
	"github.com/ethereum/go-ethereum/contracts/native/go_abi/cross_chain_manager_abi"
	"github.com/ethereum/go-ethereum/contracts/native/go_abi/signature_manager_abi"
	"github.com/ethereum/go-ethereum/contracts/native/utils"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/onflow/cadence/runtime"
	"github.com/onflow/flow-go-sdk/client"
	"github.com/onflow/flow-go-sdk/crypto"
	"github.com/onflow/flow-go/crypto/hash"
	gocrypto "github.com/onflow/flow-go/fvm/crypto"
	"github.com/polynetwork/flow-voter/config"
	"github.com/polynetwork/flow-voter/pkg/db"
	"github.com/polynetwork/flow-voter/pkg/log"
	"github.com/zhiqiangxu/util"
	"google.golang.org/grpc"
)

type Voter struct {
	signer       *ZionSigner
	conf         *config.Config
	clients      []*client.Client
	zionClients  []*ethclient.Client
	contracts    []*cross_chain_manager_abi.CrossChainManager
	contractAddr common.Address
	bdb          *db.BoltDB
	pk           crypto.PrivateKey
	hasher       hash.Hasher
	idx          int
	zidx         int
	chainID      *big.Int
}

func New(conf *config.Config) *Voter {
	return &Voter{conf: conf}
}

func (v *Voter) init() (err error) {
	signer, err := NewZionSigner(v.conf.ZionConfig.NodeKey)
	if err != nil {
		panic(err)
	}
	v.signer = signer

	pkHex := hex.EncodeToString(zionPrivateKey2Hex(v.signer.PrivateKey))
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

	var zionClients []*ethclient.Client
	for _, url := range v.conf.ZionConfig.RestURL {
		c, err := ethclient.Dial(url)
		if err != nil {
			log.Fatalf("zion ethclient.Dial failed:%v", err)
		}

		zionClients = append(zionClients, c)
	}
	v.zionClients = zionClients

	start := time.Now()
	chainID, err := zionClients[0].ChainID(context.Background())
	if err != nil {
		log.Fatalf("zionClients[0].ChainID failed:%v", err)
	}
	v.chainID = chainID
	log.Infof("ChainID() took %v", time.Now().Sub(start).String())

	// check
	path := v.conf.BoltDbPath
	if _, err := os.Stat(path); err != nil {
		log.Infof("db path not exists: %s, make dir", path)
		err := os.MkdirAll(path, 0711)
		if err != nil {
			return err
		}
	}
	bdb, err := db.NewBoltDB(path)
	if err != nil {
		return
	}

	v.bdb = bdb

	v.contractAddr = common.HexToAddress(v.conf.ZionConfig.ECCMContractAddress)
	v.contracts = make([]*cross_chain_manager_abi.CrossChainManager, len(zionClients))
	for i := 0; i < len(v.clients); i++ {
		contract, err := cross_chain_manager_abi.NewCrossChainManager(v.contractAddr, v.zionClients[i])
		if err != nil {
			return err
		}
		v.contracts[i] = contract
	}
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
		v.monitorZion(ctx)
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
		err = rlp.DecodeBytes(rawParam, param)
		if err != nil {
			log.Warnf("DecodeString rawParam failed:%v", err)
			return
		}

		if !v.conf.IsWhitelistMethod(param.Method) {
			log.Warnf("target contract method invalid %s, height: %d", param.Method, height)
			continue
		}

		empty = false
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
	for {
		duration := time.Second * 30
		timerCtx, cancelFunc := context.WithTimeout(context.Background(), duration)
		receipt, err := v.zionClients[v.zidx].TransactionReceipt(timerCtx, common.HexToHash(txHash))
		cancelFunc()
		if receipt == nil || err != nil {
			if time.Since(start) > time.Minute*5 {
				err = fmt.Errorf("waitTx timeout")
				return err
			}
			time.Sleep(time.Second)
			continue
		}
		break
	}
	return
}

func (v *Voter) commitVote(height uint32, value []byte, txhash []byte) (string, error) {
	log.Infof("commitVote, height: %d, value: %s, txhash: %s", height, hex.EncodeToString(value), hex.EncodeToString(txhash))

	duration := time.Second * 30
	timerCtx, cancelFunc := context.WithTimeout(context.Background(), duration)
	defer cancelFunc()
	client := v.zionClients[v.zidx]
	gasPrice, err := client.SuggestGasPrice(timerCtx)
	if err != nil {
		return "", fmt.Errorf("commitVote, SuggestGasPrice failed:%v", err)
	}
	ccmAbi, err := abi.JSON(strings.NewReader(cross_chain_manager_abi.CrossChainManagerABI))
	if err != nil {
		return "", fmt.Errorf("commitVote, abi.JSON error:" + err.Error())
	}
	txData, err := ccmAbi.Pack("importOuterTransfer", v.conf.FlowConfig.SideChainId, height, []byte{}, []byte{}, value, []byte{})
	if err != nil {
		panic(fmt.Errorf("commitVote, scmAbi.Pack error:" + err.Error()))
	}

	callMsg := ethereum.CallMsg{
		From: v.signer.Address, To: &utils.CrossChainManagerContractAddress, Gas: 0, GasPrice: gasPrice,
		Value: big.NewInt(0), Data: txData,
	}
	gasLimit, err := client.EstimateGas(timerCtx, callMsg)
	if err != nil {
		return "", fmt.Errorf("commitVote, client.EstimateGas failed:%v", err)
	}

	nonce := v.getNonce(v.signer.Address)
	tx := types.NewTx(&types.LegacyTx{Nonce: nonce, GasPrice: gasPrice, Gas: gasLimit,
		To: &utils.CrossChainManagerContractAddress, Value: big.NewInt(0), Data: txData})
	s := types.LatestSignerForChainID(v.chainID)
	signedtx, err := types.SignTx(tx, s, v.signer.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("commitVote, SignTransaction failed:%v", err)
	}
	err = client.SendTransaction(timerCtx, signedtx)
	if err != nil {
		return "", fmt.Errorf("commitVote, SendTransaction failed:%v", err)
	}
	h := signedtx.Hash()
	log.Infof("commitVote - send transaction to zion chain: ( zion_txhash: %s, side_txhash: %s, height: %d )",
		h.Hex(), common.BytesToHash(txhash).String(), height)

	return h.Hex(), nil
}

var ONT_USEFUL_BLOCK_NUM = uint64(1)

func (v *Voter) getZionStartHeight() (startHeight uint64) {
	startHeight = v.conf.ForceConfig.PolyHeight
	if startHeight > 0 {
		return
	}

	startHeight = v.bdb.GetPolyHeight()
	if startHeight > 0 {
		return
	}

	startHeight, err := zionGetCurrentHeight(v.conf.ZionConfig.RestURL[v.zidx])
	if err != nil {
		log.Fatalf("polySdk.GetCurrentBlockHeight failed:%v", err)
	}
	return
}

func (v *Voter) monitorZion(ctx context.Context) {

	ticker := time.NewTicker(time.Second)
	nextHeight := v.getZionStartHeight()

	for {
		select {
		case <-ticker.C:
			height, err := zionGetCurrentHeight(v.conf.ZionConfig.RestURL[v.zidx])
			if err != nil {
				log.Errorf("monitorZion GetCurrentBlockHeight failed:%v", err)
				continue
			}
			height--
			if height < nextHeight+ONT_USEFUL_BLOCK_NUM {
				log.Infof("monitorZion height(%d) < nextHeight(%d)+ONT_USEFUL_BLOCK_NUM(%d)", height, nextHeight, ONT_USEFUL_BLOCK_NUM)
				continue
			}

			for nextHeight < height-ONT_USEFUL_BLOCK_NUM {
				select {
				case <-ctx.Done():
					log.Info("monitorZion quiting from signal...")
					return
				default:
				}
				log.Infof("handling zion height:%d", nextHeight)
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

func (v *Voter) handleMakeTxEvents(height uint64) error {
	contract := v.contracts[v.idx]
	v.zidx = randIdx(len(v.zionClients))

	opt := &bind.FilterOpts{
		Start:   height,
		End:     &height,
		Context: context.Background(),
	}
	events, err := contract.FilterMakeProof(opt)
	if err != nil {
		return err
	}

	empty := true
	for events.Next() {
		evt := events.Event
		if evt.Raw.Address != v.contractAddr {
			log.Warnf("event source contract invalid: %s, expect: %s, height: %d", evt.Raw.Address.Hex(), v.contractAddr.Hex(), height)
			continue
		}
		empty = false
		param := new(common2.ToMerkleValue)
		value, err := hex.DecodeString(evt.MerkleValueHex)
		if err != nil {
			return err
		}
		err = rlp.DecodeBytes(value, param)

		var sig []byte
		sig, err = v.sign4Flow(value)
		if err != nil {
			return fmt.Errorf("sign4Flow failed:%v", err)
		}

		var txHash string
		txHash, err = v.commitSig(height, value, sig)
		if err != nil {
			return fmt.Errorf("sign4Flow failed:%v", err)
		}
		err = v.waitTx(txHash)
		if err != nil {
			return fmt.Errorf("handleMakeTxEvents failed:%v", err)
		}
	}

	log.Infof("zion height %d empty: %v", height, empty)
	return nil
}

func (v *Voter) sign4Flow(data []byte) (sig []byte, err error) {
	sig, err = v.pk.Sign([]byte(data), v.hasher)
	return
}

func (v *Voter) commitSig(height uint64, subject, sig []byte) (txHash string, err error) {
	duration := time.Second * 30
	timerCtx, cancelFunc := context.WithTimeout(context.Background(), duration)
	defer cancelFunc()
	client := v.zionClients[v.zidx]
	gasPrice, err := client.SuggestGasPrice(timerCtx)
	if err != nil {
		return "", fmt.Errorf("commitSig, SuggestGasPrice failed:%v", err)
	}
	ccmAbi, err := abi.JSON(strings.NewReader(signature_manager_abi.SignatureManagerABI))
	if err != nil {
		return "", fmt.Errorf("commitSig, abi.JSON error:" + err.Error())
	}
	txData, err := ccmAbi.Pack("addSignature", v.signer.Address, v.conf.FlowConfig.SideChainId, subject, sig)
	if err != nil {
		panic(fmt.Errorf("commitSig, scmAbi.Pack error:" + err.Error()))
	}

	callMsg := ethereum.CallMsg{
		From: v.signer.Address, To: &utils.SignatureManagerContractAddress, Gas: 0, GasPrice: gasPrice,
		Value: big.NewInt(0), Data: txData,
	}
	gasLimit, err := client.EstimateGas(timerCtx, callMsg)
	if err != nil {
		return "", fmt.Errorf("commitSig, client.EstimateGas failed:%v", err)
	}

	nonce := v.getNonce(v.signer.Address)
	tx := types.NewTx(&types.LegacyTx{Nonce: nonce, GasPrice: gasPrice, Gas: gasLimit, To: &utils.CrossChainManagerContractAddress, Value: big.NewInt(0), Data: txData})
	s := types.LatestSignerForChainID(v.chainID)
	signedtx, err := types.SignTx(tx, s, v.signer.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("commitSig, SignTransaction failed:%v", err)
	}
	err = client.SendTransaction(timerCtx, signedtx)
	if err != nil {
		return "", fmt.Errorf("commitSig, SendTransaction failed:%v", err)
	}
	h := signedtx.Hash()
	txHash = h.Hex()
	log.Infof("commitSig, height: %d, txhash: %s", height, txHash)
	return
}

func (v *Voter) getNonce(addr common.Address) uint64 {
	for {
		nonce, err := v.zionClients[v.zidx].NonceAt(context.Background(), addr, nil)
		if err != nil {
			log.Errorf("NonceAt failed:%v", err)
			sleep()
			continue
		}
		return nonce
	}
}
