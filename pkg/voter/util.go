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
	"math/rand"
	"time"

	"github.com/ontio/ontology-crypto/ec"
	"github.com/ontio/ontology-crypto/keypair"
	"github.com/polynetwork/poly/common"
)

func polyPrivateKey2Hex(pri keypair.PrivateKey) []byte {
	switch t := pri.(type) {
	case *ec.PrivateKey:
		switch t.Algorithm {
		case ec.ECDSA:

		default:
			panic("unsupported pk")
		}
		Nlen := (t.Params().BitSize + 7) >> 3
		skBytes := t.D.Bytes()
		skEncoded := make([]byte, Nlen)
		// pad sk with zeroes
		copy(skEncoded[Nlen-len(skBytes):], skBytes)
		return skEncoded
	default:
		panic("unkown private key type")
	}
}

func sleep() {
	time.Sleep(time.Second)
}

func randIdx(size int) int {
	return int(rand.Uint32()) % size
}

func parseAuditpath(path []byte) ([]byte, []byte, [][32]byte, error) {
	source := common.NewZeroCopySource(path)
	/*
		l, eof := source.NextUint64()
		if eof {
			return nil, nil, nil, nil
		}
	*/
	value, eof := source.NextVarBytes()
	if eof {
		return nil, nil, nil, nil
	}
	size := int((source.Size() - source.Pos()) / common.UINT256_SIZE)
	pos := make([]byte, 0)
	hashs := make([][32]byte, 0)
	for i := 0; i < size; i++ {
		f, eof := source.NextByte()
		if eof {
			return nil, nil, nil, nil
		}
		pos = append(pos, f)

		v, eof := source.NextHash()
		if eof {
			return nil, nil, nil, nil
		}
		var onehash [32]byte
		copy(onehash[:], (v.ToArray())[0:32])
		hashs = append(hashs, onehash)
	}

	return value, pos, hashs, nil
}
