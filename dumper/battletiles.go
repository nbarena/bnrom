package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"

	"github.com/yumland/bnrom/battletiles"
	"github.com/yumland/bnrom/paletted"
	"github.com/yumland/pngchunks"
	"golang.org/x/sync/errgroup"
)

const tileWidth = 5 * 8
const tileHeight = 3 * 8

func dumpBattleTiles(r io.ReadSeeker, outFn string) error {
	palbanks, err := battletiles.ReadPalbanks(r)
	if err != nil {
		return err
	}

	redPal, m := battletiles.ConsolidatePalbank(palbanks, battletiles.RedTileByIndex)
	bluePal, _ := battletiles.ConsolidatePalbank(palbanks, battletiles.BlueTileByIndex)

	tiles, err := battletiles.ReadTiles(r)
	if err != nil {
		return err
	}

	img := image.NewPaletted(image.Rect(0, 0, 9*tileWidth, 200*tileHeight), nil)

	idx := 0
	for j, tileImg := range tiles {
		for _, pIndex := range battletiles.RedTileByIndex[j/3] {
			tileImgCopy := battletiles.ShiftPalette(tileImg, m[pIndex])

			x := (idx % 9) * tileWidth
			y := (idx / 9) * tileHeight

			paletted.DrawOver(img, image.Rect(x, y, x+tileWidth, y+tileHeight), tileImgCopy, image.Point{})
			idx++
		}
	}
	img = img.SubImage(paletted.FindTrim(img)).(*image.Paletted)

	img.Palette = redPal
	outf, err := os.Create(outFn)
	if err != nil {
		return err
	}

	pipeR, pipeW := io.Pipe()

	var g errgroup.Group

	g.Go(func() error {
		defer pipeW.Close()
		if err := png.Encode(pipeW, img); err != nil {
			return err
		}
		return nil
	})

	pngr, err := pngchunks.NewReader(pipeR)
	if err != nil {
		return err
	}

	pngw, err := pngchunks.NewWriter(outf)
	if err != nil {
		return err
	}

	for {
		chunk, err := pngr.NextChunk()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
		}

		if err := pngw.WriteChunk(chunk.Length(), chunk.Type(), chunk); err != nil {
			return err
		}

		if chunk.Type() == "tRNS" {
			// Pack metadata in here.
			{
				var buf bytes.Buffer
				buf.WriteString("alt")
				buf.WriteByte('\x00')
				buf.WriteByte('\x08')
				for _, c := range bluePal {
					binary.Write(&buf, binary.LittleEndian, c.(color.RGBA))
					buf.WriteByte('\xff')
					buf.WriteByte('\xff')
				}
				if err := pngw.WriteChunk(int32(buf.Len()), "sPLT", bytes.NewBuffer(buf.Bytes())); err != nil {
					return err
				}
			}

			{
				var buf bytes.Buffer
				buf.WriteString("fctrl")
				buf.WriteByte('\x00')
				buf.WriteByte('\xff')
				for tileIdx, fi := range battletiles.FrameInfos {
					action := uint8(0)
					if fi.IsEnd {
						action = 0x01
					}

					x := (tileIdx % 9) * tileWidth
					y := (tileIdx / 9) * tileHeight

					binary.Write(&buf, binary.LittleEndian, struct {
						Left    int16
						Top     int16
						Right   int16
						Bottom  int16
						OriginX int16
						OriginY int16
						Delay   uint8
						Action  uint8
					}{
						int16(x),
						int16(y),
						int16(x + tileWidth),
						int16(y + tileHeight),
						int16(0),
						int16(0),
						uint8(fi.Delay),
						action,
					})

					tileIdx++
				}
				if err := pngw.WriteChunk(int32(buf.Len()), "zTXt", bytes.NewBuffer(buf.Bytes())); err != nil {
					return err
				}
			}
		}

		if err := chunk.Close(); err != nil {
			return err
		}
	}

	if err := g.Wait(); err != nil {
		return err
	}

	return nil
}