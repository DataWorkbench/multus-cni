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

	return LevelDB.Delete(getPodRefNicKey(nicID), nil)
}

func getPodRefNicKey(nicID string) []byte {
	return []byte(constants.PodRefPrefix + nicID)
}

func GetPodsByRefNicID(nicID string) ([]string, error) {
	key := getPodRefNicKey(nicID)

	value, err := LevelDB.Get(key, nil)
	if err != nil {
		return nil, err
	}

	podUniNames := []string{}
	err = json.Unmarshal(value, &podUniNames)
	if err != nil {
		return nil, err
	}

	return podUniNames, nil
}

func AddRefPodInfo(nicID, podName, namespace string) error {
	key := getPodRefNicKey(nicID)
	podUniName := GetPodUniName(podName, namespace)
	hasKey, err := LevelDB.Has(key, nil)
	if err != nil {
		return err
	}

	if !hasKey {
		value, err := json.Marshal([]string{podUniName})
		if err != nil {
			return err
		}
		return LevelDB.Put(key, value, nil)
	}

	podInfoJson, err := LevelDB.Get(key, nil)
	if err != nil {
		return err
	}

	oldPodUniNames := []string{}
	err = json.Unmarshal(podInfoJson, &oldPodUniNames)
	if err != nil {
		return err
	}

	for _, _pod := range oldPodUniNames {
		if _pod == podUniName {
			return nil
		}
	}

	oldPodUniNames = append(oldPodUniNames, podUniName)
	value, err := json.Marshal(oldPodUniNames)
	if err != nil {
		return err
	}
	return LevelDB.Put(key, value, nil)
}

func GetPodUniName(podName, namespace string) string {
	return namespace + "|" + podName
}

// set nic reference pod array empty here
// wait for Sync thread to release Nic resource
func DeleteRefPodInfo(nicID, podName, namespace string) (err error) {
	key := getPodRefNicKey(nicID)

	hasKey, err := LevelDB.Has(key, nil)
	if err != nil {
		return
	}

	if !hasKey {
		err = logging.Errorf("No Reference pod found for RefNicKey %s", key)
		return
	}

	podUniName := GetPodUniName(podName, namespace)
	podUniNameJson, err := LevelDB.Get(key, nil)
	if err != nil {
		return
	}

	oldPodUniNames := []string{}
	err = json.Unmarshal(podUniNameJson, &oldPodUniNames)
	if err != nil {
		return
	}

	newPodUniNames := []string{}
	for _, _pod := range oldPodUniNames {
		if _pod != podUniName {
			newPodUniNames = append(newPodUniNames, _pod)
		}
	}

	if len(oldPodUniNames) == len(newPodUniNames) {
		logging.Verbosef("No valid Nic found for pod %s in DB", podUniName)
		return
	}

	value, err := json.Marshal(newPodUniNames)
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
