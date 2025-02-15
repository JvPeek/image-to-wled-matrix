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
	"math"
	"net"
	"os"
	"strconv"

	"net/http"
	"sync"
	"time"

	//"github.com/Hundemeier/go-sacn/sacn" //completely broken do not use
	"gitlab.com/patopest/go-sacn"
	"gitlab.com/patopest/go-sacn/packet"
	"golang.org/x/image/draw"

	//_ "golang.org/x/image/webp"
	"github.com/gen2brain/webp"
	"github.com/joho/godotenv"
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
type Config struct {
	RES_H       int
	RES_V       int
	TARGET_IP   string
	SOURCE_PORT int
}

var config Config

func decodeImage(img image.Image, bounds image.Rectangle) Frame {
	resizedFrame := image.NewRGBA(
		image.Rect(0, 0, config.RES_H, config.RES_V),
	)

	draw.NearestNeighbor.Scale(
		resizedFrame,
		resizedFrame.Bounds(),
		img,
		bounds,
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
		bounds := image.Rect(0, 0, conf.Width, conf.Height)
		if imgFormat == "gif" {

			//hier sind deine frames
			inputGIF, err := gif.DecodeAll(imgReader)
			if err != nil {
				a.returnError(w, err)
				return
			}
			//bounds := inputGIF.Image[0].Bounds()
			//in inputGIF.Image liegen die frames

			fmt.Printf("GIF Anzahl der Frames:%v\n", len(inputGIF.Image))
			frameImage := FrameImage{}
			frameImage.FrameTimes = inputGIF.Delay
			for index, time := range frameImage.FrameTimes {
				frameImage.FrameTimes[index] = time * 10
				if frameImage.FrameTimes[index] < 10 {
					frameImage.FrameTimes[index] = 10
				}
			}
			//for frame in gif frames:
			//geht aber auch:
			for i, img := range inputGIF.Image {
				fmt.Printf("Decoding GIF Frame: %v with size: %v vs %v\n", i, img.Bounds(), bounds)
				if inputGIF.Disposal[i] == gif.DisposalNone && i > 0 {
					combinedBounds := inputGIF.Image[i-1].Bounds().Union(img.Bounds())
					tempImg := image.NewPaletted(combinedBounds, img.Palette)
					draw.Copy(tempImg, inputGIF.Image[i-1].Bounds().Min, inputGIF.Image[i-1], inputGIF.Image[i-1].Bounds(), draw.Src, nil)
					draw.Copy(tempImg, img.Bounds().Min, img, img.Bounds(), draw.Src, nil)
					inputGIF.Image[i] = tempImg
					img = tempImg
				}
				frameImage.Frames = append(frameImage.Frames, decodeImage(img, bounds))
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
			//bounds := webp.Image[0].Bounds()
			for _, img := range webp.Image {
				frameImage.Frames = append(frameImage.Frames, decodeImage(img, bounds))
			}
			frameImage.FrameTimes = webp.Delay
			for index, time := range frameImage.FrameTimes {
				if time < 10 {
					frameImage.FrameTimes[index] = 10
				}
			}
			fmt.Printf("WEBP Anzahl der Frames:%v\n", len(frameImage.Frames))
			a.queue = append(a.queue, frameImage)
		} else {
			//ansonsten
			img, _, err := image.Decode(imgReader)
			if err != nil {
				a.returnError(w, err)
			}
			frameImage := FrameImage{}
			frameImage.Frames = append(frameImage.Frames, decodeImage(img, bounds))
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

	err := http.ListenAndServe(":"+strconv.Itoa(config.SOURCE_PORT), nil)
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
	pix_per_universes := 170.0
	pixels := float64(config.RES_H * config.RES_V)

	var universes uint16 = uint16(math.Ceil(pixels / pix_per_universes))
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
			sender.SetDestinations(universe, []string{config.TARGET_IP})
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

		sleptTime := 0

	endloop:
		for {
			for index, frame := range frames {
				displaySingleFrame(frame)
				time.Sleep(time.Duration(image.FrameTimes[index]) * time.Millisecond)
				sleptTime += image.FrameTimes[index]
				if sleptTime >= 10_000 {
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

		enableOutput()

		displayImage()

		//no 10sek sleep between images
		//time.Sleep(10000 * time.Millisecond)
		/* output gets disabled via defer aka on exit
		 */
		if len(a.queue) == 0 {
			disableOutput()
		}
		time.Sleep(1000 * time.Millisecond)
	}
}
func checkConfig() Config {

	thisConfig := Config{}
	err := godotenv.Load()
	if err != nil {
		fmt.Println(".env file not loaded")
	}

	RES_H, err := strconv.Atoi(os.Getenv("RES_H"))
	if err != nil {
		// ... handle error
		panic(err)
	}
	RES_V, err := strconv.Atoi(os.Getenv("RES_V"))
	if err != nil {
		// ... handle error
		panic(err)
	}
	thisConfig.RES_H = RES_H
	thisConfig.RES_V = RES_V

	TARGET_IP := os.Getenv("TARGET_IP")
	if net.ParseIP(TARGET_IP) == nil {
		panic("not a valid IP address")
	}
	thisConfig.TARGET_IP = TARGET_IP

	SOURCE_PORT, err := strconv.Atoi(os.Getenv("SOURCE_PORT"))
	if err != nil {
		// ... handle error
		panic(err)
	}
	if SOURCE_PORT > 65535 || SOURCE_PORT < 1 {
		panic("not a valid port number")
	}
	thisConfig.SOURCE_PORT = SOURCE_PORT
	fmt.Printf("RES_H: %v, RESV: %v\nTARGET_IP: %v\nSOURCE_PORT: %v\n", RES_H, RES_V, TARGET_IP, SOURCE_PORT)

	return thisConfig
}
func main() {

	config = checkConfig()
	app := &QueueApp{}

	go app.sendToArtnet()
	if err := app.Serve(); err != nil {
		panic(err)
	}
}
