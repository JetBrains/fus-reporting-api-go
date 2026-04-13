package fus

import (
	"os"

	"golang.org/x/sys/windows"
)

// Windows has no flock(2). LockFileEx with LOCKFILE_EXCLUSIVE_LOCK gives
// equivalent exclusive advisory locking; passing 0xFFFFFFFF for both the
// low and high length words locks the entire file.
func lockFile(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		0xFFFFFFFF,
		0xFFFFFFFF,
		&ol,
	)
}

func unlockFile(f *os.File) error {
	var ol windows.Overlapped
	return windows.UnlockFileEx(
		windows.Handle(f.Fd()),
		0,
		0xFFFFFFFF,
		0xFFFFFFFF,
		&ol,
	)
}
