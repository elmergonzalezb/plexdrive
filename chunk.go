package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"time"

	. "github.com/claudetech/loggo/default"
)

// ChunkManager manages chunks on disk
type ChunkManager struct {
	ChunkPath       string
	ChunkSize       int64
	downloadManager *DownloadManager
}

type ChunkRequest struct {
	Object      *APIObject
	Offset      int64
	Size        int64
	Preload     bool
	id          string
	fOffset     int64
	offsetStart int64
	offsetEnd   int64
}

type ChunkResponse struct {
	Error error
	Bytes []byte
}

// NewChunkManager creates a new chunk manager
func NewChunkManager(downloadManager *DownloadManager, chunkPath string, chunkSize int64) (*ChunkManager, error) {
	if "" == chunkPath {
		return nil, fmt.Errorf("Path to chunk file must not be empty")
	}
	if chunkSize < 4096 {
		return nil, fmt.Errorf("Chunk size must not be < 4096")
	}
	if chunkSize%1024 != 0 {
		return nil, fmt.Errorf("Chunk size must be divideable by 1024")
	}

	manager := ChunkManager{
		ChunkPath:       chunkPath,
		ChunkSize:       chunkSize,
		downloadManager: downloadManager,
	}

	return &manager, nil
}

func (m *ChunkManager) RequestChunk(req *ChunkRequest) <-chan *ChunkResponse {
	res := make(chan *ChunkResponse)

	go func() {
		defer close(res)

		req.fOffset = req.Offset % m.ChunkSize
		req.offsetStart = req.Offset - req.fOffset
		req.offsetEnd = req.offsetStart + m.ChunkSize
		req.id = fmt.Sprintf("%v:%v", req.Object.ObjectID, req.offsetStart)

		diskRes := m.loadChunkFromDisk(req)
		if nil != diskRes.Error {
			Log.Debugf("%v", diskRes.Error)
		} else {
			res <- diskRes
		}

		apiRes := m.downloadManager.RequestChunk(req)

		if nil == apiRes.Error {
			sOffset := int64(math.Min(float64(req.fOffset), float64(len(apiRes.Bytes))))
			eOffset := int64(math.Min(float64(req.fOffset+req.Size), float64(len(apiRes.Bytes))))
			res <- &ChunkResponse{
				Bytes: apiRes.Bytes[sOffset:eOffset],
			}

			m.storeChunkToDisk(req, apiRes)
		} else {
			res <- apiRes
		}
	}()

	return res
}

func (m *ChunkManager) loadChunkFromDisk(req *ChunkRequest) *ChunkResponse {
	chunkDir := filepath.Join(m.ChunkPath, req.Object.ObjectID)
	filename := filepath.Join(chunkDir, strconv.Itoa(int(req.offsetStart)))

	f, err := os.Open(filename)
	if nil != err {
		Log.Tracef("%v", err)
		return &ChunkResponse{
			Error: fmt.Errorf("Could not open file %v", filename),
		}
	}
	defer f.Close()

	buf := make([]byte, req.Size)
	n, err := f.ReadAt(buf, req.fOffset)
	if n > 0 && (nil == err || io.EOF == err || io.ErrUnexpectedEOF == err) {
		Log.Tracef("Found file %s bytes %v - %v in cache", filename, req.offsetStart, req.offsetEnd)

		// update the last modified time for files that are often in use
		if err := os.Chtimes(filename, time.Now(), time.Now()); nil != err {
			Log.Warningf("Could not update last modified time for %v", filename)
		}

		eOffset := int64(math.Min(float64(req.Size), float64(len(buf))))
		return &ChunkResponse{
			Bytes: buf[:eOffset],
		}
	}

	Log.Tracef("%v", err)
	return &ChunkResponse{
		Error: fmt.Errorf("Could not read file %s at %v", filename, req.fOffset),
	}
}

func (m *ChunkManager) storeChunkToDisk(req *ChunkRequest, res *ChunkResponse) {
	chunkDir := filepath.Join(m.ChunkPath, req.Object.ObjectID)
	filename := filepath.Join(chunkDir, strconv.Itoa(int(req.offsetStart)))

	if _, err := os.Stat(chunkDir); os.IsNotExist(err) {
		if err := os.MkdirAll(chunkDir, 0777); nil != err {
			Log.Debugf("%v", err)
			Log.Warningf("Could not create chunk temp path %v", chunkDir)
		}
	}

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		if err := ioutil.WriteFile(filename, res.Bytes, 0777); nil != err {
			Log.Debugf("%v", err)
			Log.Warningf("Could not write chunk temp file %v", filename)
		}
	}
}
