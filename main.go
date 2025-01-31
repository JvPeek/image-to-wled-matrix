package main

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"

	"net/http"
	"sync"
	"time"

	"github.com/Hundemeier/go-sacn/sacn"
	"golang.org/x/exp/rand"
	"golang.org/x/image/draw"
)

type QueueApp struct {
	queue []*gif.GIF
	mu    sync.RWMutex
}

func (a *QueueApp) returnError(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte(fmt.Sprintf("something went wrong: %s", err)))
}

func (a *QueueApp) handleAdd(w http.ResponseWriter, req *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, url := range req.URL.Query()["image"] {
		resp, err := http.Get(url)
		if err != nil {
			a.returnError(w, err)
			return
		}

		// Wir speichern die Image-Daten erstmal zwischen, weil wir die evtl. nochmal brauchen
		imgData, err := io.ReadAll(resp.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(fmt.Sprintf("something went wrong: %s", err)))
			return
		}

		// Wir brauchen für die Decoder aber wieder einen io.Reader
		imgReader := bytes.NewReader(imgData)

		// den verwenden wir nun hier wie zuvor resp.Body
		img, imgFormat, err := image.Decode(imgReader)
		if err != nil {
			a.returnError(w, err)

			return
		}

		// wenn es kein GIF ist, machen wir eins daraus
		if imgFormat != "gif" {
			var newImage bytes.Buffer
			err = gif.Encode(&newImage, img, nil)
			if err != nil {
				a.returnError(w, err)
			}

			// und jetzt können wir ja einfach unsere imageDaten überschreiben

			imgReader = bytes.NewReader(newImage.Bytes())

		}

		// jetzt spulen wir nochmal zurück, weil wir ja nicht auf 0 wären wenn es schon ein gif war
		imgReader.Seek(0, io.SeekStart)

		// hier ist unser Input ja jetzt immer ein GIF

		inputGIF, err := gif.DecodeAll(imgReader)
		if err != nil {
			a.returnError(w, err)
			return
		}

		newWidth := 32
		newHeight := 32

		outputGIF := &gif.GIF{
			LoopCount: inputGIF.LoopCount,
			Config: image.Config{
				ColorModel: inputGIF.Config.ColorModel,
				Width:      newWidth,
				Height:     newHeight,
			},
		}

		for i, frame := range inputGIF.Image {
			resizedFrame := image.NewPaletted(
				image.Rect(0, 0, newWidth, newHeight),
				frame.Palette,
			)

			draw.NearestNeighbor.Scale(
				resizedFrame,
				resizedFrame.Bounds(),
				frame,
				frame.Bounds(),
				draw.Over,
				nil,
			)

			outputGIF.Image = append(outputGIF.Image, resizedFrame)
			outputGIF.Delay = append(outputGIF.Delay, inputGIF.Delay[i])
		}

		a.queue = append(a.queue, outputGIF)
	}

	fmt.Fprint(w, "Image added")
}

func (a *QueueApp) handleShow(w http.ResponseWriter, req *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.queue) == 0 {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Geh weg"))

		return
	}

	data := a.queue[0]
	//a.queue = a.queue[1:]

	w.Header().Set("Content-Type", "image/gif")
	// imaging.Encode(w, data, imaging.GIF)
	gif.EncodeAll(w, data)

}
func (a *QueueApp) handleStat(w http.ResponseWriter, req *http.Request) {
	w.Write([]byte(fmt.Sprintf("current queue: %v", len(a.queue))))

}
func (a *QueueApp) Serve() error {
	http.HandleFunc("/add", a.handleAdd)
	http.HandleFunc("/show", a.handleShow)
	http.HandleFunc("/stat", a.handleStat)

	err := http.ListenAndServe(":8090", nil)
	return err
}

func (a *QueueApp) sendToArtnet() error {
	//instead of "" you could provide an ip-address that the socket should bind to
	trans, err := sacn.NewTransmitter("", [16]byte{1, 2, 3}, "test")
	if err != nil {
		log.Fatal(err)
	}

	// Map to store active channels
	channels := make(map[uint16]chan<- []byte)

	// Activate each universe
	//UNIVERSE ANZAHL DEFINITION HIER
	var universes uint16 = 7
	//HIER DRÜBER
	//iterate over 0..universe-1

	enableOutput := func() {
		fmt.Println("enabling output")
		for i := range universes {
			universe := i + 1 //universes are 1 indexed
			if trans.IsActivated(universe) {
				continue
			}
			ch, err := trans.Activate(universe)

			if err != nil {
				log.Fatalf("Failed to activate universe %d: %v", universe, err)
			}
			channels[universe] = ch
			trans.SetDestinations(universe, []string{"192.168.2.90"})
		}
	}
	disableOutput := func() {
		fmt.Println("disabling output")
		for i := range universes {
			if ch := channels[i+1]; ch != nil {
				close(ch)
			}
		}
	}

	displayImage := func() {
		a.mu.Lock()
		//get image data
		// data := a.queue[0]
		a.queue = a.queue[1:]
		a.mu.Unlock()
		//actual send
		//for range data or so send one frame
	}

	for {
		if len(a.queue) == 0 {
			disableOutput()
			time.Sleep(1000 * time.Millisecond)
			continue
		}

		enableOutput()

		displayImage()
		//1. get current image
		//loop for 10 seconds
		//get current frame
		for universe, ch := range channels {
			data := [510]byte{}

			for i := range 510 {
				data[i] = byte(rand.Intn(2) * 255) // Channel 1

			}
			// Send the data
			ch <- data[:]

			log.Printf("Sent data to Universe %d", universe)
		}

		time.Sleep(10000 * time.Millisecond)
	}
}

func main() {
	app := &QueueApp{}

	go app.sendToArtnet()
	if err := app.Serve(); err != nil {
		panic(err)
	}
}
