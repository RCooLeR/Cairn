package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
)

func main() {
	input := flag.String("input", "", "source PNG icon")
	linuxDir := flag.String("linux-dir", "", "output Linux icon root")
	name := flag.String("name", "cairn", "icon name")
	flag.Parse()

	if *input == "" || *linuxDir == "" {
		fatalf("-input and -linux-dir are required")
	}

	file, err := os.Open(*input)
	if err != nil {
		fatalf("open source icon: %v", err)
	}
	defer file.Close()

	src, _, err := image.Decode(file)
	if err != nil {
		fatalf("decode source icon: %v", err)
	}

	for _, size := range []int{16, 24, 32, 48, 64, 128, 256, 512} {
		dst := resizeContain(src, size)
		out := filepath.Join(*linuxDir, "hicolor", fmt.Sprintf("%dx%d", size, size), "apps", *name+".png")
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			fatalf("create %s: %v", filepath.Dir(out), err)
		}
		if err := writePNG(out, dst); err != nil {
			fatalf("write %s: %v", out, err)
		}
	}
}

func resizeContain(src image.Image, size int) image.Image {
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	if srcW <= 0 || srcH <= 0 {
		return dst
	}

	scaleW := float64(size) / float64(srcW)
	scaleH := float64(size) / float64(srcH)
	scale := scaleW
	if scaleH < scale {
		scale = scaleH
	}
	drawW := int(float64(srcW)*scale + 0.5)
	drawH := int(float64(srcH)*scale + 0.5)
	if drawW < 1 {
		drawW = 1
	}
	if drawH < 1 {
		drawH = 1
	}
	offsetX := (size - drawW) / 2
	offsetY := (size - drawH) / 2

	for y := 0; y < drawH; y++ {
		sy := bounds.Min.Y + y*srcH/drawH
		for x := 0; x < drawW; x++ {
			sx := bounds.Min.X + x*srcW/drawW
			dst.Set(offsetX+x, offsetY+y, src.At(sx, sy))
		}
	}
	return dst
}

func writePNG(path string, img image.Image) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, img)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
