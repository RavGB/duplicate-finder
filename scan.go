package main

// How the finder works, and why its totals stay exact
//
// Phase 1 (walk): a worker pool reads the folder tree one directory
// at a time, the same shape as a directory scan. Workers never touch
// shared state: each directory produces one dirResult on a results
// channel and a single aggregator merges them, so counts are exact
// and race-free. An outstanding-jobs counter (Add before enqueue,
// Done after processing, a closer goroutine that closes the jobs
// channel at zero) ends the walk exactly when the last directory is
// done. Enqueue sends run in their own short-lived goroutines so a
// full jobs channel can never deadlock workers handing off children.
//
// Counting rules for the walk: symlinked directories are never
// followed, and every symlink (to a file or a directory) is skipped
// and counted, because following one could count another tree's
// bytes as this tree's duplicates. Other non-regular entries
// (sockets, devices, named pipes) are skipped and counted as
// special files. A directory that cannot be read, or an entry that
// cannot be stat'ed, adds to the read-error count with a short path
// sample; it never aborts the scan. The one exception is the scan
// root itself: if it cannot be read, the scan is unusable and the
// caller reports a fatal error.
//
// Phase 2 (hash): only files whose size is shared by at least one
// other eligible file can possibly be duplicates, so only those
// candidates are hashed. Hashing streams the file through SHA-256
// with a fixed-size buffer; a whole file is never loaded into
// memory. A second worker pool hashes candidates in parallel.
//
// A file could change while it is being scanned. Hashing a file
// whose size no longer matches its phase 1 size would silently
// produce a wrong group, so every hashed file is stat'ed again
// right after hashing: if the size changed (or the file vanished or
// stopped being a regular file), the file is dropped from grouping
// and counted as changed during the scan. It is never guessed at.

import (
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// fileEntry is one regular file found by the walk.
type fileEntry struct {
	Path string
	Size int64
}

// scanStats holds every counter the report's summary needs.
type scanStats struct {
	FilesScanned     int64
	CandidatesHashed int64
	SkippedSymlinks  int64
	SkippedEmpty     int64
	SkippedSpecial   int64
	ChangedFiles     int64
	ReadErrors       int64
	ErrorSample      []string
}

const errorSampleCap = 10

func (s *scanStats) noteError(path string) {
	s.ReadErrors++
	if len(s.ErrorSample) < errorSampleCap {
		s.ErrorSample = append(s.ErrorSample, path)
	}
}

// finder carries the scan settings plus a replaceable hash function,
// so tests can count hash calls and simulate files changing mid-scan.
type finder struct {
	workers      int
	minSize      int64
	includeEmpty bool
	hashFn       func(path string) ([sha256.Size]byte, error)
}

func newFinder(workers int, minSize int64, includeEmpty bool) *finder {
	if workers < 1 {
		workers = 1
	}
	return &finder{
		workers:      workers,
		minSize:      minSize,
		includeEmpty: includeEmpty,
		hashFn:       sha256File,
	}
}

// sha256File streams a file through SHA-256. The contents pass
// through a fixed-size buffer, so memory use does not grow with the
// size of the file.
func sha256File(path string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.CopyBuffer(h, f, make([]byte, 64*1024)); err != nil {
		return sum, err
	}
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// dirResult is everything one directory job found.
type dirResult struct {
	dir      string
	files    []fileEntry
	symlinks int64
	special  int64
	errPaths []string
	rootErr  error // non-nil only when the scan root itself failed
}

// collectFiles walks the tree rooted at root with a worker pool and
// returns every regular file in it. A failure to read the root
// itself is returned as a fatal error; every other read problem is
// folded into the stats.
func (fd *finder) collectFiles(root string) ([]fileEntry, *scanStats, error) {
	stats := &scanStats{}
	jobs := make(chan string)
	results := make(chan dirResult)

	var outstanding sync.WaitGroup
	var workersWG sync.WaitGroup
	for w := 0; w < fd.workers; w++ {
		workersWG.Add(1)
		go func() {
			defer workersWG.Done()
			for dir := range jobs {
				res := fd.readDir(dir, root)
				for _, sub := range res.subdirs {
					outstanding.Add(1)
					go func(p string) {
						jobs <- p
					}(sub)
				}
				results <- res.result
			}
		}()
	}
	go func() {
		workersWG.Wait()
		close(results)
	}()

	outstanding.Add(1)
	go func() { jobs <- root }()
	go func() {
		outstanding.Wait()
		close(jobs)
	}()

	var files []fileEntry
	var fatal error
	for res := range results {
		outstanding.Done()
		if res.rootErr != nil {
			fatal = res.rootErr
			continue
		}
		files = append(files, res.files...)
		stats.SkippedSymlinks += res.symlinks
		stats.SkippedSpecial += res.special
		for _, p := range res.errPaths {
			stats.noteError(p)
		}
	}
	if fatal != nil {
		return nil, stats, fatal
	}
	stats.FilesScanned = int64(len(files))
	return files, stats, nil
}

// dirWork pairs the public result of reading one directory with the
// subdirectories it discovered, which only the pool needs.
type dirWork struct {
	result  dirResult
	subdirs []string
}

// readDir reads exactly one directory. It never returns an error:
// problems are recorded on the result, with the scan root's own
// failure marked as fatal for the caller to see.
func (fd *finder) readDir(dir, root string) dirWork {
	work := dirWork{result: dirResult{dir: dir}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if dir == root {
			work.result.rootErr = err
		} else {
			work.result.errPaths = append(work.result.errPaths, dir)
		}
		return work
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		mode := entry.Type()
		switch {
		case mode&os.ModeSymlink != 0:
			// Files and directories alike: never followed.
			work.result.symlinks++
		case entry.IsDir():
			work.subdirs = append(work.subdirs, path)
		case mode.IsRegular():
			info, err := entry.Info()
			if err != nil {
				work.result.errPaths = append(work.result.errPaths, path)
				continue
			}
			work.result.files = append(work.result.files, fileEntry{Path: path, Size: info.Size()})
		default:
			work.result.special++
		}
	}
	return work
}

// hashResult is one candidate's outcome from the hash pool.
type hashResult struct {
	entry   fileEntry
	hash    [sha256.Size]byte
	changed bool
	failed  bool
}

// hashCandidates hashes every candidate file with a worker pool and
// verifies each file's size again afterwards, dropping any file that
// changed during the scan. Outcomes arrive on a results channel and
// are merged by the single caller goroutine.
func (fd *finder) hashCandidates(candidates []fileEntry, stats *scanStats) map[int64]map[[sha256.Size]byte][]string {
	jobs := make(chan int)
	results := make(chan hashResult)

	var wg sync.WaitGroup
	for w := 0; w < fd.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				entry := candidates[i]
				results <- fd.hashOne(entry)
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()
	go func() {
		for i := range candidates {
			jobs <- i
		}
		close(jobs)
	}()

	grouped := make(map[int64]map[[sha256.Size]byte][]string)
	for res := range results {
		stats.CandidatesHashed++
		switch {
		case res.failed:
			stats.noteError(res.entry.Path)
		case res.changed:
			stats.ChangedFiles++
		default:
			byHash, ok := grouped[res.entry.Size]
			if !ok {
				byHash = make(map[[sha256.Size]byte][]string)
				grouped[res.entry.Size] = byHash
			}
			byHash[res.hash] = append(byHash[res.hash], res.entry.Path)
		}
	}
	return grouped
}

// hashOne hashes one file and re-stats it. The result is marked
// failed when the file cannot be read, and changed when its size no
// longer matches the size the walk recorded (or it is no longer a
// regular file), so a moving file can never land in a wrong group.
func (fd *finder) hashOne(entry fileEntry) hashResult {
	sum, err := fd.hashFn(entry.Path)
	if err != nil {
		return hashResult{entry: entry, failed: true}
	}
	info, err := os.Stat(entry.Path)
	if err != nil {
		return hashResult{entry: entry, failed: true}
	}
	if !info.Mode().IsRegular() || info.Size() != entry.Size {
		return hashResult{entry: entry, changed: true}
	}
	return hashResult{entry: entry, hash: sum}
}

// find runs both phases over root and builds the report. A fatal
// error means the scan root was unusable; everything else is in the
// report's counters.
func (fd *finder) find(root string) (*Report, error) {
	files, stats, err := fd.collectFiles(root)
	if err != nil {
		return nil, err
	}

	// Filter to eligible files and group them by size. Only a size
	// shared by two or more files can hide a duplicate.
	bySize := make(map[int64][]fileEntry)
	for _, f := range files {
		if f.Size == 0 && !fd.includeEmpty {
			stats.SkippedEmpty++
			continue
		}
		if f.Size < fd.minSize {
			continue
		}
		bySize[f.Size] = append(bySize[f.Size], f)
	}
	var candidates []fileEntry
	for _, same := range bySize {
		if len(same) >= 2 {
			candidates = append(candidates, same...)
		}
	}

	grouped := fd.hashCandidates(candidates, stats)
	return buildReport(root, grouped, stats), nil
}
