package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/fujiwara/shapeio"
	"github.com/grafov/m3u8"
	"github.com/rs/cors"
)

// How much time to project into the future
const futureTime = 5.0

type FakeLHLSManifestHandler struct {
	startTime time.Time
	duration  float64
	playlist  *m3u8.MediaPlaylist
	baseDir   string
}

func (l *FakeLHLSManifestHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if strings.HasSuffix(req.URL.EscapedPath(), "manifest.m3u8") {
		l.ServeManifest(w, req)
	} else if strings.HasPrefix(req.URL.EscapedPath(), "/lhls/") {
		l.ServeSegment(w, req)
	} else {
		http.NotFound(w, req)
	}
}

func (l *FakeLHLSManifestHandler) ServeSegment(w http.ResponseWriter, req *http.Request) {
	curSegmentURL := req.URL.EscapedPath()[len("/lhls/"):]
	curFilePath := path.Join(l.baseDir, curSegmentURL)

	file, err := os.Open(curFilePath) // For read access.
	if err != nil {
		log.Println("Failed to open file:", err)
		http.NotFound(w, req)
		return
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		log.Println("Failed to stat file:", err)
		http.NotFound(w, req)
		return
	}

	var segment *m3u8.MediaSegment
	segmentStartTime := 0.0
	for _, curSegment := range l.playlist.Segments {
		if strings.EqualFold(curSegment.URI, curSegmentURL) {
			segment = curSegment
			break
		}
		segmentStartTime += curSegment.Duration
	}

	if segment == nil {
		http.NotFound(w, req)
		return
	}

	curStreamTime := time.Since(l.startTime).Seconds()
	if segmentStartTime > curStreamTime {
		sleepDuration := segmentStartTime - curStreamTime
		fmt.Println("Segment is in the future, waiting for", sleepDuration)
		time.Sleep(time.Duration(sleepDuration) * time.Second)

		curStreamTime = segmentStartTime
	}

	rateDuration := math.Max(segmentStartTime+segment.Duration-curStreamTime, 1.0)
	rateLimit := float64(fileInfo.Size()) / rateDuration

	fmt.Printf("Serving segment %s with duration %f ratelimit %f\n", curSegmentURL, rateDuration, rateLimit)

	w.Header().Set("Content-Type", "video/MP2T")
	w.WriteHeader(http.StatusOK)

	writer := shapeio.NewWriter(w)
	writer.SetRateLimit(rateLimit) // Roughly realtime transfer
	io.Copy(writer, file)

	return
}

func (l *FakeLHLSManifestHandler) ServeManifest(w http.ResponseWriter, req *http.Request) {
	curStreamTime := time.Since(l.startTime).Seconds()
	fmt.Println("Generating playlist, curStreamTime: ", curStreamTime)

	curPlaylist, err := m3u8.NewMediaPlaylist(10, 10)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("oops"))
		return
	}

	// XXX: Reset stream if duration has passed
	if l.duration < curStreamTime {
		curStreamTime = 0
		l.startTime = time.Now()
	}

	curDuration := 0.0
	sequence := -1
	for i, segment := range l.playlist.Segments {
		curDuration += segment.Duration

		if (curDuration + (3 * l.playlist.TargetDuration)) > curStreamTime {
			if sequence == -1 {
				sequence = i
			}
			curPlaylist.AppendSegment(segment)
		}

		if curDuration > (curStreamTime + futureTime) {
			break
		}
	}

	w.Header().Set("Content-Type", "application/x-mpegURL")
	w.WriteHeader(200)
	curPlaylist.SeqNo = uint64(sequence)
	w.Write(curPlaylist.Encode().Bytes())
}

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) != 1 {
		fmt.Println("Usage: lhls_faker input.m3u8")
	}

	f, err := os.Open(args[0])
	if err != nil {
		fmt.Println("Failed to open input", err)
		return
	}

	p, listType, err := m3u8.DecodeFrom(bufio.NewReader(f), true)
	if err != nil {
		fmt.Println("Failed to decode m3u8", err)
		return
	}

	if listType != m3u8.MEDIA {
		fmt.Println("Script only supports media playlists")
		return
	}

	segmentDir := filepath.Dir(args[0])
	fmt.Println("Segmentdir: ", segmentDir)

	mediapl := p.(*m3u8.MediaPlaylist)
	duration := 0.0
	for _, segment := range mediapl.Segments {
		if segment != nil {
			segmentLocation := segment.URI
			if !path.IsAbs(segmentLocation) {
				segmentLocation = path.Join(segmentDir, segment.URI)
			}
			duration += segment.Duration
		}
	}
	fmt.Println("Duration: ", duration)

	mux := http.NewServeMux()

	lhlsHandler := &FakeLHLSManifestHandler{
		duration:  duration,
		startTime: time.Now(),
		playlist:  mediapl,
		baseDir:   path.Dir(args[0]),
	}

	// XXX: For testing, "/live/manifest" serves up a manifest that will work 'normally'
	mux.Handle("/live/manifest.m3u8", lhlsHandler)
	mux.Handle("/live/", http.StripPrefix("/live/", http.FileServer(http.Dir(path.Dir(args[0])))))

	// XXX: "/lhls/manifest" servces up a manifest where segments will behave like LHLS segments
	mux.Handle("/lhls/manifest.m3u8", lhlsHandler)
	mux.Handle("/lhls/", lhlsHandler)

	corsHandler := cors.Default().Handler(mux)
	log.Fatal(http.ListenAndServe(":8080", corsHandler))
}
