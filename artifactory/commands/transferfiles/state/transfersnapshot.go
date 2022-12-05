package state

import (
	"github.com/jfrog/jfrog-cli-core/v2/utils/reposnapshot"
	"github.com/jfrog/jfrog-client-go/utils/errorutils"
	"sync"
	"time"
)

var saveRepoSnapshotMutex sync.Mutex

type SnapshotActionFunc func(rts *RepoTransferSnapshot) error

var SaveSnapshotIntervalMin = snapshotSaveIntervalMinDefault

const snapshotSaveIntervalMinDefault = 10

// RepoTransferSnapshot handles saving and loading the repository's transfer snapshot.
type RepoTransferSnapshot struct {
	snapshotManager   reposnapshot.RepoSnapshotManager
	lastSaveTimestamp time.Time
	// This boolean marks that this snapshot continues a previous run. It allows skipping certain checks if it was not loaded, because some data is known to be new.
	loadedFromSnapshot bool
}

// Runs the provided action on the snapshot manager, and periodically saves the rep state and snapshot to the snapshot dir.
func (ts *TransferStateManager) snapshotAction(action SnapshotActionFunc) error {
	if ts.repoTransferSnapshot == nil {
		return errorutils.CheckErrorf("invalid call to snapshot manager before it was initialized")
	}
	if err := action(ts.repoTransferSnapshot); err != nil {
		return err
	}

	now := time.Now()
	if now.Sub(ts.repoTransferSnapshot.lastSaveTimestamp).Minutes() < float64(SaveSnapshotIntervalMin) {
		return nil
	}

	if !saveRepoSnapshotMutex.TryLock() {
		return nil
	}
	defer saveRepoSnapshotMutex.Unlock()

	ts.repoTransferSnapshot.lastSaveTimestamp = now
	if err := ts.repoTransferSnapshot.snapshotManager.PersistRepoSnapshot(); err != nil {
		return err
	}

	return saveStateToSnapshot(&ts.TransferState)
}

func saveStateToSnapshot(ts *TransferState) error {
	saveStateMutex.Lock()
	defer saveStateMutex.Unlock()
	return ts.persistTransferState(true)
}

func (ts *TransferStateManager) LookUpNode(relativePath string) (requestedNode *reposnapshot.Node, err error) {
	err = ts.snapshotAction(func(rts *RepoTransferSnapshot) error {
		requestedNode, err = rts.snapshotManager.LookUpNode(relativePath)
		return err
	})
	return
}

func (ts *TransferStateManager) WasSnapshotLoaded() (wasLoaded bool, err error) {
	err = ts.snapshotAction(func(rts *RepoTransferSnapshot) error {
		wasLoaded = rts.loadedFromSnapshot
		return nil
	})
	return
}

func (ts *TransferStateManager) GetDirectorySnapshotNodeWithLru(relativePath string) (node *reposnapshot.Node, err error) {
	err = ts.snapshotAction(func(rts *RepoTransferSnapshot) error {
		node, err = rts.snapshotManager.GetDirectorySnapshotNodeWithLru(relativePath)
		return err
	})
	return
}

func (ts *TransferStateManager) DisableRepoTransferSnapshot() {
	ts.repoTransferSnapshot = nil
}

func (ts *TransferStateManager) IsRepoTransferSnapshotEnabled() bool {
	return ts.repoTransferSnapshot != nil
}

func LoadRepoTransferSnapshot(repoKey, snapshotFilePath string) (*RepoTransferSnapshot, bool, error) {
	snapshotManager, exists, err := reposnapshot.LoadRepoSnapshotManager(repoKey, snapshotFilePath)
	if err != nil || !exists {
		return nil, exists, err
	}
	return &RepoTransferSnapshot{snapshotManager: snapshotManager, lastSaveTimestamp: time.Now(), loadedFromSnapshot: true}, true, nil
}

func CreateRepoTransferSnapshot(repoKey, snapshotFilePath string) *RepoTransferSnapshot {
	return &RepoTransferSnapshot{snapshotManager: reposnapshot.CreateRepoSnapshotManager(repoKey, snapshotFilePath), lastSaveTimestamp: time.Now()}
}
