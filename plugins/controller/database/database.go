// Copyright (c) 2018 Cisco and/or its affiliates.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at:
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package database

import (
	"fmt"
	"github.com/gogo/protobuf/proto"
	"go.ligato.io/cn-infra/v2/datasync"
	"go.ligato.io/cn-infra/v2/db/keyval"
	"go.ligato.io/cn-infra/v2/logging/logrus"
)

var db keyval.ProtoBroker
var log *logrus.Logger

// InitDatabase initializes access variables
func InitDatabase(p keyval.ProtoBroker, l *logrus.Logger) {
	db = p
	log = l
}

// WriteToDatastore writes the specified entity to the KVStore
func WriteToDatastore(key string, data proto.Message) error {

	log.Debugf("WriteToDatastore: key: '%s', data: '%v'", key, data)

	err := db.Put(key, data)
	if err != nil {
		log.Error("WriteToDatastore: write error: ", err)
		return err
	}
	return nil
}

// ReadIterate is a utility func to iterate over a set
func ReadIterate(
	keyPrefix string,
	getDataBuffer func() proto.Message,
	actionFunc func(data proto.Message)) error {

	log.Debugf("ReadIterate: keyPrefix: %s", keyPrefix)

	kvi, err := db.ListValues(keyPrefix)
	if err != nil {
		log.Fatal(err)
		return nil
	}

	for {
		kv, allReceived := kvi.GetNext()
		if allReceived {
			log.Debugf("ReadIterate: allReceived: %v", allReceived)
			return nil
		}
		data := getDataBuffer()
		log.Debugf("ReadIterate: getDataBuffer data: %v", data)
		err := kv.GetValue(data)
		log.Debugf("ReadIterate: GetValue data: %v", data)
		if err != nil {
			log.Fatal(err)
			return nil
		}
		log.Debugf("IterateFromDatastore: key: '%s'", keyPrefix)
		log.Debugf("IterateFromDatastore: data=%v", data)

		actionFunc(data)
	}
}

// ReadFromDatastore reads the specified entity from the kv store
func ReadFromDatastore(key string, data proto.Message) error {

	log.Debugf("ReadFromDatastore: key: '%s'", key)

	found, _, err := db.GetValue(key, data)
	if err != nil {
		return err
	}
	if !found {
		err = fmt.Errorf("ReadFromDatastore: key not found '%s', key", key)
	}
	return err
}

// DeleteFromDatastore removes the specified entry from KVStore etcd
func DeleteFromDatastore(key string) {
	log.Debugf("DeleteFromDatastore: key: '%s'", key)
	if _, err := db.Delete(key); err != nil {
		log.Errorf("DeleteFromDatastore failed: %v", err)
	}
}

// CleanDatastore removes all entries from /sfc-controller and below
func CleanDatastore(treePrefix string) {
	log.Debugf("CleanDatastore: clearing etc tree")
	if _, err := db.Delete(treePrefix, datasync.WithPrefix()); err != nil {
		log.Errorf("CleanDatastore failed: %v", err)
	}
}
