package lsh

import (
	"errors"
	"github.com/gasparian/lsh-search-go/store"
	"sync"
)

var (
	distanceErr = errors.New("Distance can't be calculated")
)

// Record holds vector and it's unique identifier generated by `user`
type Record struct {
	ID  string
	Vec []float64
}

type lshConfig struct {
	DistanceMetric int
	DistanceThrsh  float64
	MaxNN          int
	Mean           []float64
	Std            []float64
}

// Config holds all needed constants for creating the Hasher instance
type Config struct {
	lshConfig
	hasherConfig
}

// LSHIndex holds buckets with vectors and hasher instance
type LSHIndex struct {
	config lshConfig
	index  store.Store
	hasher *hasher
}

// New creates new instance of hasher and index, where generated hashes will be stored
func New(config Config, store store.Store) (*LSHIndex, error) {
	hasher := &hasher{
		config: hasherConfig{
			NPermutes:      config.NPermutes,
			NPlanes:        config.NPlanes,
			BiasMultiplier: config.BiasMultiplier,
			Dims:           config.Dims,
		},
		instances: make([]hasherInstance, config.NPermutes),
	}
	err := hasher.generate(config.Mean, config.Std)
	if err != nil {
		return nil, err
	}
	return &LSHIndex{
		config: lshConfig{
			DistanceMetric: config.DistanceMetric,
			DistanceThrsh:  config.DistanceThrsh,
			MaxNN:          config.MaxNN,
			Mean:           config.Mean,
			Std:            config.Std,
		},
		hasher: hasher,
		index:  store,
	}, nil
}

// Train fills new search index with vectors
func (lsh *LSHIndex) Train(records []Record) error {
	err := lsh.index.Clear()
	if err != nil {
		return err
	}
	wg := sync.WaitGroup{}
	wg.Add(len(records))
	for _, record := range records {
		go func(record Record, wg *sync.WaitGroup) {
			hashes := lsh.hasher.getHashes(record.Vec)
			lsh.index.SetVector(record.ID, record.Vec)
			for perm, hash := range hashes {
				lsh.index.SetHash(perm, hash, record.ID)
			}
			wg.Done()
		}(record, &wg)
	}
	wg.Wait()
	return nil
}

// Search returns NNs for the query point
func (lsh *LSHIndex) Search(query []float64) ([]Record, error) {
	hashes := lsh.hasher.getHashes(query)
	closestSet := NewStringSet()
	errs := make(chan error, len(hashes))
	closest := make(chan Record, len(hashes))
	defer close(errs)
	defer close(closest)

	distanceMetric := lsh.config.DistanceMetric
	distanceThrsh := lsh.config.DistanceThrsh
	maxNN := lsh.config.MaxNN
	for perm, hash := range hashes {
		go func(perm int, hash uint64) {
			if len(closest) == maxNN {
				return
			}
			iter, err := lsh.index.GetHashIterator(perm, hash)
			if err != nil {
				errs <- err
				return
			}
			for id, err := iter.Next(); err == nil; {
				if closestSet.Get(id) {
					continue
				}
				vec, err := lsh.index.GetVector(id)
				if err != nil {
					errs <- err
					return
				}
				var dist float64 = -1
				switch distanceMetric {
				case Cosine:
					dist = CosineSim(vec, query)
				case Euclidian:
					dist = L2(vec, query)
				}
				if dist < 0 {
					errs <- distanceErr
					return
				}
				if dist <= distanceThrsh {
					closestSet.Set(id)
					closest <- Record{ID: id, Vec: vec}
					if len(closest) == maxNN {
						return
					}
				}
			}
		}(perm, hash)
	}
	closestArr := make([]Record, 0)
	for range hashes {
		select {
		case res := <-closest:
			closestArr = append(closestArr, res)
		case err := <-errs:
			if err != nil {
				return nil, err
			}
		}
	}
	return closestArr, nil
}

// DumpHasher serializes hasher
func (lsh *LSHIndex) DumpHasher() ([]byte, error) {
	return lsh.hasher.dump()
}

// LoadHasher fills hasher from byte array
func (lsh *LSHIndex) LoadHasher(inp []byte) error {
	return lsh.hasher.load(inp)
}
