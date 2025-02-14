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

	//"github.com/Hundemeier/go-sacn/sacn" //completely broken do not use
	"gitlab.com/patopest/go-sacn"
	"gitlab.com/patopest/go-sacn/packet"
	"golang.org/x/image/draw"

	//_ "golang.org/x/image/webp"
	"github.com/gen2brain/webp"
)

/*
 TODO. problem für zukunfts-jan:

- Multiframe-anzeige während konfigurierbarer dauer.
*/

type QueueApp struct {
	queue []FrameImage
	mu    sync.RWMutex
}

func (a *QueueApp) returnError(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusInternalServerError)
	w.Write([]byte(fmt.Sprintf("something went wrong: %s", err)))
}

type FrameImage struct {
	Frames     []Frame
	FrameTimes []int //anzeige dauer eines einzelnen frames
}

type Frame struct {
	//r,g,b,a slice
	Pixels []byte
}

func decodeImage(img image.Image) Frame {
	resizedFrame := image.NewRGBA(
		image.Rect(0, 0, 32, 32),
	)

	draw.NearestNeighbor.Scale(
		resizedFrame,
		resizedFrame.Bounds(),
		img,
		img.Bounds(),
		draw.Over,
		nil,
	)

	rect := resizedFrame.Bounds()
	pixels := make([]byte, (rect.Max.X-rect.Min.X)*(rect.Max.Y-rect.Min.Y)*4)
	index := 0
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			r, g, b, a := resizedFrame.At(x, y).RGBA()
			pixels[index] = byte(r % 256)
			pixels[index+1] = byte(g % 256)
			pixels[index+2] = byte(b % 256)
			pixels[index+3] = byte(a % 256)
			index += 4
		}
	}
	return Frame{pixels}
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
			w.Write([]byte(fmt.Sprintf("something went wrong: %s %s", err, resp.Body)))
			return
		}

		// Wir brauchen für die Decoder aber wieder einen io.Reader
		imgReader := bytes.NewReader(imgData)

		// den verwenden wir nun hier wie zuvor resp.Body
		//eigentlich gehts hier nur ums format damit wir gif gesondert parsen können
		conf, imgFormat, err := image.DecodeConfig(imgReader)
		fmt.Printf("Image format: %v %v\n", imgFormat, conf)
		if err != nil {
			a.returnError(w, err)

			return
		}

		//decoding steps:
		//1. decode image by type (gif with gifDecoder, png with pngDecoder etc)
		//2. rescale to 32x32
		//3. create new FrameImage von den image frames
		/*Example:
		rescaledGif := ...
		framedImage := FrameImage{}
		for _, image := range outputGIF.Image {
			framedImage.Frames = append(framedImage.Frames, image)
		}
		*/
		//triggert der auch bei mir??

		//auf 0 zurückspulen
		imgReader.Seek(0, io.SeekStart)
		// wenn es ein GIF ist, decoden wir alle frames mit gif.DecodeAll(imgReader)
		if imgFormat == "gif" {

			//hier sind deine frames
			inputGIF, err := gif.DecodeAll(imgReader)
			if err != nil {
				a.returnError(w, err)
				return
			}
			//in inputGIF.Image liegen die frames

			fmt.Printf("GIF Anzahl der Frames:%v\n", len(inputGIF.Image))
			frameImage := FrameImage{}
			frameImage.FrameTimes = inputGIF.Delay
			for index, time := range frameImage.FrameTimes {
				frameImage.FrameTimes[index] = time * 10
			}
			//40fps
			//frameImage.FrameTime = (1000 / 40) * time.Millisecond
			//for frame in gif frames:
			//geht aber auch:
			for _, img := range inputGIF.Image {
				frameImage.Frames = append(frameImage.Frames, decodeImage(img))
			}
			//end for

			a.queue = append(a.queue, frameImage)

		} else if imgFormat == "webp" {
			if false {
				fmt.Println("webp decode broken atm, come back later")
				return
			}
			frameImage := FrameImage{}
			webp, err := webp.DecodeAll(imgReader)
			//send error if first decode fails
			if err != nil {
				a.returnError(w, err)
			}
			for _, img := range webp.Image {
				frameImage.Frames = append(frameImage.Frames, decodeImage(img))
			}
			frameImage.FrameTimes = webp.Delay
			fmt.Printf("WEBP Anzahl der Frames:%v\n", len(frameImage.Frames))
			a.queue = append(a.queue, frameImage)
		} else {
			//ansonsten
			img, _, err := image.Decode(imgReader)
			if err != nil {
				a.returnError(w, err)
			}
			frameImage := FrameImage{}
			frameImage.Frames = append(frameImage.Frames, decodeImage(img))
			frameImage.FrameTimes = []int{500}
			a.queue = append(a.queue, frameImage)
		}
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

	//data := a.queue[0]
	//a.queue = a.queue[1:]

	w.Header().Set("Content-Type", "image/gif")
	// imaging.Encode(w, data, imaging.GIF)
	//gif.EncodeAll(w, data)

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

	opts := sacn.SenderOptions{ // Default for all packets sent by Sender if not provided in the packet itself.
	}
	//instead of "" you could provide an ip-address that the socket should bind to
	sender, err := sacn.NewSender("", &opts) // Create sender with binding to interface
	if err != nil {
		log.Fatal(err)
	}
	// Map to store active channels
	//universe_map := make(map[uint16]chan<- packet.SACNPacket)

	// Activate each universe
	//UNIVERSE ANZAHL DEFINITION HIER
	var universes uint16 = 7
	//HIER DRÜBER
	//iterate over 0..universe-1

	mapMutex := sync.Mutex{}
	enableOutput := func() {
		mapMutex.Lock()
		defer mapMutex.Unlock()

		fmt.Println("enabling output")
		for i := range universes {
			universe := i + 1 //universes are 1 indexed
			if sender.IsEnabled(universe) {
				continue
			}
			_, err := sender.StartUniverse(universe)

			if err != nil {
				log.Fatalf("Failed to activate universe %d: %v", universe, err)
			}
			sender.SetDestinations(universe, []string{"192.168.2.90"})
			//hatten wir multicast?
			//sender.SetMulticast(universe, true)
		}
	}
	disableOutput := func() {
		mapMutex.Lock()
		defer mapMutex.Unlock()

		fmt.Println("disabling output")

		for i := range universes {
			sender.StopUniverse(i + 1)
		}
	}
	enableOutput()
	defer disableOutput()

	/*
		Display Strategie:
		call displayImage
		for frame: call displayFrame
		wait frameTime or x ms if single frame
	*/
	displaySingleFrame := func(frame Frame) {
		maxPos := len(frame.Pixels)
		pixels_per_universe := 170
		for i := range universes {
			universe := i + 1
			data := [510]byte{}
			for j := range pixels_per_universe {
				//0 ist der 0/1te frame
				pos := (int(i)*pixels_per_universe + j) * 4
				if pos >= maxPos {
					break
				}
				//frameColor0 := frames[0].Palette[frame0Indices[int(i)*pixels_per_universe+j]]
				//fmt.Printf("PixelIndex: %v\n", int(i)*pixels_per_universe+j)
				ir := frame.Pixels[pos]
				ig := frame.Pixels[pos+1]
				ib := frame.Pixels[pos+2]
				ia := frame.Pixels[pos+3]
				ia = ia / 255.0
				data[j*3] = byte(ir * ia)
				data[j*3+1] = byte(ig * ia)
				data[j*3+2] = byte(ib * ia)

			}

			// for i := range 510 {
			// 	data[i] = byte(rand.Intn(2) * 255) // Channel 1

			// }

			// Send the data
			p := packet.NewDataPacket()
			p.SetData(data[:])
			sender.Send(universe, p)

			log.Printf("Sent data to Universe %d", universe)
		}

	}
	displayImage := func() {
		a.mu.Lock()
		//get image data
		image := a.queue[0]
		a.queue = a.queue[1:]
		frames := image.Frames
		a.mu.Unlock()
		//frame0Indices := frames[0].Pix

		//1. get current image

		//TODO loop for 10 seconds
		timeNow := time.Now()

		timeEnd := timeNow.Add(time.Second * 10)

	endloop:
		for {
			for index, frame := range frames {
				displaySingleFrame(frame)
				time.Sleep(time.Duration(image.FrameTimes[index]) * time.Millisecond)
				if time.Now().Unix() > timeEnd.Unix() {
					break endloop
				}
			}

		}

		//actual send
		//for range data or so send one frame
	}

	for {
		if len(a.queue) == 0 {
			time.Sleep(1000 * time.Millisecond)
			continue
		}

		//enableOutput()

		displayImage()

		//no 10sek sleep between images
		//time.Sleep(10000 * time.Millisecond)
		/* output gets disabled via defer aka on exit
		if len(a.queue) == 0 && false {
			disableOutput()
		}
		*/
		time.Sleep(1000 * time.Millisecond)
	}
}

func main() {
	app := &QueueApp{}

	go app.sendToArtnet()
	if err := app.Serve(); err != nil {
		panic(err)
	}
}
