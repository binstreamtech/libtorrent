package libtorrent

// #include <stdlib.h>
import "C"

import (
	"bufio"
	"bytes"
	"errors"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

var (
	builtinAnnounceList = [][]string{
		{"udp://tracker.openbittorrent.com:80"},
		{"udp://tracker.kicks-ass.net:80/announce"},
	}
)

func SetDefaultAnnouncesList(str string) {
	mu.Lock()
	defer mu.Unlock()

	builtinAnnounceList = nil
	for _, s := range strings.Split(str, "\n") {
		builtinAnnounceList = append(builtinAnnounceList, []string{s})
	}
}

//export CreateTorrentFile
func CreateTorrentFile(path string) []byte {
	mi := &metainfo.MetaInfo{
		AnnounceList: builtinAnnounceList,
	}
	mi.SetDefaults()
	err = mi.Info.BuildFromFilePath(path)
	if err != nil {
		return nil
	}
	var b bytes.Buffer
	w := bufio.NewWriter(&b)
	err = mi.Write(w)
	if err != nil {
		return nil
	}
	err = w.Flush()
	if err != nil {
		return nil
	}
	return b.Bytes()
}

// Create
//
// Create libtorrent object
//
//export Create
func Create() bool {
	mu.Lock()
	defer mu.Unlock()

	torrents = make(map[int]*torrent.Torrent)
	filestorage = make(map[metainfo.Hash]*fileStorage)
	queue = make(map[*torrent.Torrent]int64)
	index = 0

	clientConfig.DefaultStorage = &torrentOpener{}
	clientConfig.Seed = true
	clientConfig.ListenAddr = ":0"

	client, err = torrent.NewClient(&clientConfig)
	if err != nil {
		return false
	}

	clientAddr = client.ListenAddr().String()

	// when create client do 1 second discovery
	mu.Unlock()
	mapping(1 * time.Second)
	mu.Lock()

	err = client.Start()
	if err != nil {
		return false
	}

	go func() {
		for {
			select {
			case <-client.Wait():
				return
			case <-time.After(refreshPort):
			}
			// in go routine do 5 seconds discovery
			mapping(5 * time.Second)
		}
	}()

	return true
}

type BytesInfo struct {
	Downloaded int64
	Uploaded   int64
}

func Stats() *BytesInfo {
	stats := client.Stats()
	return &BytesInfo{stats.Downloaded, stats.Uploaded}
}

// Get Torrent Count
//
//export Count
func Count() int {
	mu.Lock()
	defer mu.Unlock()

	return len(torrents)
}

//export ListenAddr
func ListenAddr() string {
	return client.ListenAddr().String()
}

//export CreateTorrent
func CreateTorrent(p string) int {
	mu.Lock()
	defer mu.Unlock()

	var t *torrent.Torrent

	mi := &metainfo.MetaInfo{
		AnnounceList: builtinAnnounceList,
	}

	mi.SetDefaults()

	err = mi.Info.BuildFromFilePath(p)
	if err != nil {
		return -1
	}

	mi.Info.UpdateBytes()

	if _, ok := filestorage[mi.Info.Hash()]; ok {
		err = errors.New("Already exists")
		return -1
	}

	fs := createFileStorage(path.Dir(p))

	fs.Comment = mi.Comment
	fs.Creator = mi.CreatedBy
	fs.CreatedOn = mi.CreationDate

	filestorage[mi.Info.Hash()] = fs

	t, err = client.AddTorrent(mi)
	if err != nil {
		return -1
	}

	fileUpdateCheck(t)

	return register(t)
}

// AddMagnet
//
// Add magnet link to download list
//
//export AddMagnet
func AddMagnet(path string, magnet string) int {
	mu.Lock()
	defer mu.Unlock()

	var t *torrent.Torrent
	var spec *torrent.TorrentSpec

	spec, err = torrent.TorrentSpecFromMagnetURI(magnet)
	if err != nil {
		return -1
	}

	if _, ok := filestorage[spec.InfoHash]; ok {
		err = errors.New("Already exists")
		return -1
	}

	filestorage[spec.InfoHash] = createFileStorage(path)

	t, _, err = client.AddTorrentSpec(spec)
	if err != nil {
		return -1
	}

	return register(t)
}

// AddTorrent
//
// Add torrent from local file or remote url.
//
//export AddTorrentFromURL
func AddTorrentFromURL(path string, url string) int {
	var t *torrent.Torrent
	var mi *metainfo.MetaInfo

	var resp *http.Response
	resp, err = http.Get(url)
	if err != nil {
		return -1
	}
	defer resp.Body.Close()

	mi, err = metainfo.Load(resp.Body)
	if err != nil {
		return -1
	}

	mu.Lock()
	defer mu.Unlock()

	if _, ok := filestorage[mi.Info.Hash()]; ok {
		err = errors.New("Already exists")
		return -1
	}

	fs := createFileStorage(path)

	fs.Comment = mi.Comment
	fs.Creator = mi.CreatedBy
	fs.CreatedOn = mi.CreationDate

	filestorage[mi.Info.Hash()] = fs

	t, err = client.AddTorrent(mi)
	if err != nil {
		return -1
	}

	fileUpdateCheck(t)

	return register(t)
}

// AddTorrent
//
// Add torrent from local file or remote url.
//
//export AddTorrentFromFile
func AddTorrentFromFile(path string, file string) int {
	var t *torrent.Torrent
	var mi *metainfo.MetaInfo

	mi, err = metainfo.LoadFromFile(file)
	if err != nil {
		return -1
	}

	mu.Lock()
	defer mu.Unlock()

	if _, ok := filestorage[mi.Info.Hash()]; ok {
		err = errors.New("Already exists")
		return -1
	}

	fs := createFileStorage(path)

	fs.Comment = mi.Comment
	fs.Creator = mi.CreatedBy
	fs.CreatedOn = mi.CreationDate

	filestorage[mi.Info.Hash()] = fs

	t, err = client.AddTorrent(mi)
	if err != nil {
		return -1
	}

	fileUpdateCheck(t)

	return register(t)
}

//export AddTorrentFromBytes
func AddTorrentFromBytes(path string, buf []byte) int {
	var t *torrent.Torrent
	var mi *metainfo.MetaInfo

	r := bytes.NewReader(buf)

	mi, err = metainfo.Load(r)
	if err != nil {
		return -1
	}

	mu.Lock()
	defer mu.Unlock()

	if _, ok := filestorage[mi.Info.Hash()]; ok {
		err = errors.New("Already exists")
		return -1
	}

	fs := createFileStorage(path)

	fs.Comment = mi.Comment
	fs.Creator = mi.CreatedBy
	fs.CreatedOn = mi.CreationDate

	filestorage[mi.Info.Hash()] = fs

	t, err = client.AddTorrent(mi)
	if err != nil {
		return -1
	}

	fileUpdateCheck(t)

	return register(t)
}

// Get Torrent file from runtime torrent
//
//export GetTorrent
func GetTorrent(i int) []byte {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	err = t.Metainfo().Write(w)
	if err != nil {
		return nil
	}
	err = w.Flush()
	if err != nil {
		return nil
	}
	return buf.Bytes()
}

// Separate load / create torrent from network activity.
//
// Start announce torrent, seed/download
//
//export StartTorrent
func StartTorrent(i int) bool {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]

	if client.ActiveTorrent(t) {
		return true
	}

	if client.ActiveCount() >= ActiveCount {
		// priority to start, seeding torrent will not start over downloading torrents
		return queueStart(t)
	}

	return startTorrent(t)
}

func startTorrent(t *torrent.Torrent) bool {
	fs := filestorage[t.InfoHash()]

	delete(queue, t)

	err = client.StartTorrent(t)
	if err != nil {
		return false
	}

	fs.ActivateDate = time.Now().Unix()

	go func() {
		select {
		case <-t.GotInfo():
		case <-t.Wait():
			return
		}

		mu.Lock()
		defer mu.Unlock()

		now := time.Now().Unix()
		fs.DownloadingTime = fs.DownloadingTime + (now - fs.ActivateDate)
		fs.ActivateDate = now

		fileUpdateCheck(t)
	}()

	// queue engine
	go func() {
		timeout := time.Duration(QueueTimeout) * time.Second
		for {
			b1 := t.BytesCompleted()
			select {
			case <-time.After(timeout):
			case <-fs.Completed.LockedChan(&mu):
			case <-t.Wait():
				return
			}
			timeout = time.Duration(QueueTimeout) * time.Second
			mu.Lock()
			s := torrentStatus(t)
			if s == StatusSeeding {
				if queueNext(t) {
					// we been removed, stop queue engine
					mu.Unlock()
					return
				} else {
					// we not been removed
					if len(queue) != 0 {
						// queue full, some one soon be available, check every minute
						timeout = 1 * time.Minute
					}
				}
			}
			if s == StatusDownloading {
				// check stalled, and rotate if it does
				b2 := t.BytesCompleted()
				if b1 == b2 {
					if queueNext(t) {
						// we been removed, stop queue engine
						mu.Unlock()
						return
					} else {
						// we not been removed
						if len(queue) != 0 {
							// queue full, some one soon be available, check every minute
							timeout = 1 * time.Minute
						}
					}
				}
			}
			mu.Unlock()
		}
	}()

	return true
}

// Download only metadata from magnet link and stop torrent
//
//export DownloadMetadata
func DownloadMetadata(i int) bool {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	fs := filestorage[t.InfoHash()]

	if client.ActiveTorrent(t) {
		return true
	}

	err = client.StartTorrent(t)
	if err != nil {
		return false
	}

	fs.ActivateDate = time.Now().Unix()

	go func() {
		select {
		case <-t.GotInfo():
		case <-t.Wait():
			return
		}

		mu.Lock()
		defer mu.Unlock()

		now := time.Now().Unix()
		fs.DownloadingTime = fs.DownloadingTime + (now - fs.ActivateDate)
		fs.ActivateDate = now

		fileUpdateCheck(t)
		t.Drop()
	}()

	return true
}

func MetaTorrent(i int) bool {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	return t.Info() != nil
}

// Stop torrent from announce, check, seed, download
//
//export StopTorrent
func StopTorrent(i int) {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	stopTorrent(t)
	queueNext(nil)
}

func stopTorrent(t *torrent.Torrent) {
	fs := filestorage[t.InfoHash()]

	if client.ActiveTorrent(t) {
		t.Drop()

		now := time.Now().Unix()
		if t.Seeding() {
			fs.SeedingTime = fs.SeedingTime + (now - fs.ActivateDate)
		} else {
			fs.DownloadingTime = fs.DownloadingTime + (now - fs.ActivateDate)
		}
	} else {
		t.Stop()
		delete(queue, t)
	}
}

// CheckTorrent
//
// Check torrent file consisteny (pices hases) on a disk. Pause torrent if
// downloading, resume after.
//
//export CheckTorrent
func CheckTorrent(i int) {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	client.CheckTorrent(t)
}

// Remote torrent for library
//
//export RemoveTorrent
func RemoveTorrent(i int) {
	mu.Lock()
	defer mu.Unlock()

	t := torrents[i]
	if client.ActiveTorrent(t) {
		t.Drop()
	}
	unregister(i)
}

//export Error
func Error() string {
	mu.Lock()
	defer mu.Unlock()

	if err != nil {
		return err.Error()
	}
	return ""
}

//export Close
func Close() {
	mu.Lock()
	defer mu.Unlock()

	if client != nil {
		client.Close()
		client = nil
	}
}

//
// protected
//

var clientConfig torrent.Config
var client *torrent.Client
var clientAddr string
var err error
var torrents map[int]*torrent.Torrent
var index int
var mu sync.Mutex

func register(t *torrent.Torrent) int {
	fs := filestorage[t.InfoHash()]

	index++
	for torrents[index] != nil {
		index++
	}
	torrents[index] = t

	fs.t = t

	return index
}

func unregister(i int) {
	t := torrents[i]

	delete(filestorage, t.InfoHash())

	delete(torrents, i)
}
