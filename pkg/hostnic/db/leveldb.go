package db

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/constants"
	"github.com/DataWorkbench/multus-cni/pkg/hostnic/rpc"
	"github.com/DataWorkbench/multus-cni/pkg/logging"
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

func SetNetworkInfo(key string, info *rpc.NICMMessage) error {
	value, _ := json.Marshal(info)
	return LevelDB.Put([]byte(key), value, nil)
}

func DeleteNetworkInfo(nicID string) error {
	err := LevelDB.Delete([]byte(nicID), nil)
	if err == leveldb.ErrNotFound {
		return constants.ErrNicNotFound
	}

	crnKey := getContainerRefNicKey(nicID)
	return LevelDB.Delete(crnKey, nil)
}

func getContainerRefNicKey(nicID string) []byte {
	return []byte(constants.ContainerRefPrefix + nicID)
}

func GetContainerRelatedNicInfo(nicID string) ([]string, error) {
	crnKey := getContainerRefNicKey(nicID)

	value, err := LevelDB.Get(crnKey, nil)
	if err != nil {
		return nil, err
	}

	containerIDs := []string{}
	err = json.Unmarshal(value, &containerIDs)
	if err != nil {
		return nil, err
	}

	return containerIDs, nil
}

func AddRelatedContainer(nicID, containerID string) error {
	key := getContainerRefNicKey(nicID)
	hasKey, err := LevelDB.Has(key, nil)
	if err != nil {
		return err
	}

	if !hasKey {
		value, err := json.Marshal([]string{containerID})
		if err != nil {
			return err
		}
		return LevelDB.Put(key, value, nil)
	}

	containerIDJson, err := LevelDB.Get(key, nil)
	if err != nil {
		return err
	}

	oldContainerIDs := []string{}
	err = json.Unmarshal(containerIDJson, &oldContainerIDs)
	if err != nil {
		return err
	}

	for _, _containerID := range oldContainerIDs {
		if _containerID == containerID {
			return nil
		}
	}

	oldContainerIDs = append(oldContainerIDs, containerID)
	value, err := json.Marshal(oldContainerIDs)
	if err != nil {
		return err
	}
	return LevelDB.Put(key, value, nil)
}

// set nic related container array empty here
// wait for Sync thread to release Nic resource
func DeleteRelatedContainer(nicID, containerID string) (err error) {
	key := getContainerRefNicKey(nicID)
	hasKey, err := LevelDB.Has(key, nil)
	if err != nil {
		return
	}

	if !hasKey {
		err = logging.Errorf("No valid Nic found for container %s", containerID)
		return
	}

	containerIDJson, err := LevelDB.Get(key, nil)
	if err != nil {
		return
	}

	oldContainerIDs := []string{}
	err = json.Unmarshal(containerIDJson, &oldContainerIDs)
	if err != nil {
		return
	}

	newContainerIDs := []string{}
	for _, _containerID := range oldContainerIDs {
		if _containerID != containerID {
			newContainerIDs = append(newContainerIDs, _containerID)
		}
	}

	if len(oldContainerIDs) == len(newContainerIDs) {
		err = logging.Errorf("No valid Nic found for container %s in DB", containerID)
		return
	}

	value, err := json.Marshal(newContainerIDs)
	if err != nil {
		return
	}
	err = LevelDB.Put(key, value, nil)
	return
}

func Iterator(fn func(key, value []byte) error) error {
	iter := LevelDB.NewIterator(nil, nil)
	for iter.Next() {
		_ = fn(iter.Key(), iter.Value())
	}
	iter.Release()

	return iter.Error()
}
