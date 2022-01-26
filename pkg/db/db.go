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

package db

import (
	"encoding/binary"
	"errors"
	"os"
	"path"

	"github.com/boltdb/bolt"
)

type BoltDB struct {
	db       *bolt.DB
	filePath string
}

var (
	BucketHeight  = []byte("Height")
	FlowHeightKey = []byte("side_height")
	PolyHeightKey = []byte("poly_height")
)

func NewBoltDB(dir string) (bdb *BoltDB, err error) {

	if dir == "" {
		err = errors.New("db dir is empty")
		return
	}
	err = os.MkdirAll(dir, 0777)
	if err != nil {
		return
	}

	filePath := path.Join(dir, "bolt.bin")
	db, err := bolt.Open(filePath, 0644, &bolt.Options{InitialMmapSize: 500000})
	if err != nil {
		return
	}

	err = db.Update(func(btx *bolt.Tx) error {
		_, err := btx.CreateBucketIfNotExists(BucketHeight)
		return err
	})
	if err != nil {
		return
	}
	bdb = &BoltDB{db: db, filePath: filePath}
	return
}

func (w *BoltDB) UpdateFlowHeight(h uint64) error {

	raw := make([]byte, 8)
	binary.LittleEndian.PutUint64(raw, h)

	return w.db.Update(func(tx *bolt.Tx) error {
		bkt := tx.Bucket(BucketHeight)
		return bkt.Put(FlowHeightKey, raw)
	})
}

func (w *BoltDB) GetFlowHeight() uint64 {

	var h uint64
	_ = w.db.View(func(tx *bolt.Tx) error {
		bkt := tx.Bucket(BucketHeight)
		raw := bkt.Get(FlowHeightKey)
		if len(raw) == 0 {
			h = 0
			return nil
		}
		h = binary.LittleEndian.Uint64(raw)
		return nil
	})
	return h
}

func (w *BoltDB) UpdatePolyHeight(h uint32) error {

	raw := make([]byte, 4)
	binary.LittleEndian.PutUint32(raw, h)

	return w.db.Update(func(tx *bolt.Tx) error {
		bkt := tx.Bucket(BucketHeight)
		return bkt.Put(PolyHeightKey, raw)
	})
}

func (w *BoltDB) GetPolyHeight() uint32 {

	var h uint32
	_ = w.db.View(func(tx *bolt.Tx) error {
		bkt := tx.Bucket(BucketHeight)
		raw := bkt.Get(PolyHeightKey)
		if len(raw) == 0 {
			h = 0
			return nil
		}
		h = binary.LittleEndian.Uint32(raw)
		return nil
	})
	return h
}

func (w *BoltDB) Close() {
	w.db.Close()
}
