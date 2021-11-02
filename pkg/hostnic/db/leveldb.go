package db

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/DataWorkBench/multus-cni/pkg/hostnic/constants"
	"github.com/DataWorkBench/multus-cni/pkg/hostnic/rpc"
	"github.com/DataWorkBench/multus-cni/pkg/logging"
	"github.com/syndtr/goleveldb/leveldb"
)

const (
	defaultDBPath = "/var/lib/traffic-nic"
)

var (
	LevelDB *leveldb.DB
)

type LevelDBOptions struct {
	dbpath string
}

func NewLevelDBOptions() *LevelDBOptions {
	return &LevelDBOptions{
		dbpath: defaultDBPath,
	}
}

func (opt *LevelDBOptions) AddFlags() {
	flag.StringVar(&opt.dbpath, "dbpath", defaultDBPath, "set leveldb path")
}

func SetupLevelDB(opt *LevelDBOptions) error {
	db, err := leveldb.OpenFile(opt.dbpath, nil)
	if err != nil {
		return fmt.Errorf("cannot open leveldb file %s : %v", opt.dbpath, err)
	}

	LevelDB = db

	return nil
}

func CloseDB() {
	err := LevelDB.Close()
	if err != nil {
		_ = logging.Errorf("failed to close leveldb, err: %v", err)
	} else {
		logging.Verbosef("leveldb closed")
	}
}

func SetNetworkInfo(key string, info *rpc.IPAMMessage) error {
	value, _ := json.Marshal(info)
	return LevelDB.Put([]byte(key), value, nil)
}

func DeleteNetworkInfo(key string) error {
	err := LevelDB.Delete([]byte(key), nil)
	if err == leveldb.ErrNotFound {
		return constants.ErrNicNotFound
	}

	return err
}

func Iterator(fn func(info *rpc.IPAMMessage) error) error {
	iter := LevelDB.NewIterator(nil, nil)
	for iter.Next() {
		var (
			value rpc.IPAMMessage
		)
		// Remember that the contents of the returned slice should not be modified, and
		// only valid until the next call to Next.
		json.Unmarshal(iter.Value(), &value)

		fn(&value)
	}
	iter.Release()

	return iter.Error()
}
