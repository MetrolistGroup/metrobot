package discord

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/MetrolistGroup/metrobot/cmd"
	"go.uber.org/zap"
)

const (
	garminGIFMaxBytes       = 12 << 20
	garminGIFMaxFrames      = 200
	garminGIFMaxFramePixels = 40_000_000
	garminGIFMaxConversions = 3
)

func prepareGarminGIFs(ctx context.Context, client *http.Client, messages []cmd.GarminAIMessage, logger *zap.Logger) {
	converted := 0
	for messageIndex := range messages {
		messageConverted := false
		for imageIndex, imageURL := range messages[messageIndex].Images {
			if converted == garminGIFMaxConversions || !garminGIFURL(imageURL) {
				continue
			}
			dataURL, err := fetchGarminGIFMiddleFrame(ctx, client, imageURL)
			if err != nil {
				if logger != nil {
					logger.Debug("failed to prepare GIF for Metrobot vision", zap.Error(err))
				}
				continue
			}
			messages[messageIndex].Images[imageIndex] = dataURL
			converted++
			messageConverted = true
		}
		if messageConverted {
			messages[messageIndex].Content += "\n\nAnimated GIF input is represented by its middle frame."
		}
	}
}

func garminGIFURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return strings.EqualFold(path.Ext(parsed.Path), ".gif") && (host == "cdn.discordapp.com" || host == "media.discordapp.net")
}

func fetchGarminGIFMiddleFrame(ctx context.Context, client *http.Client, imageURL string) (string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("GIF returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, garminGIFMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > garminGIFMaxBytes {
		return "", fmt.Errorf("GIF exceeds %d bytes", garminGIFMaxBytes)
	}
	return garminGIFMiddleFrame(data)
}

func garminGIFMiddleFrame(data []byte) (string, error) {
	config, err := gif.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("decode GIF dimensions: %w", err)
	}
	frames, err := countGarminGIFFrames(data)
	if err != nil {
		return "", err
	}
	pixels := int64(config.Width) * int64(config.Height)
	if config.Width <= 0 || config.Height <= 0 || frames == 0 || frames > garminGIFMaxFrames || pixels*int64(frames) > garminGIFMaxFramePixels {
		return "", fmt.Errorf("GIF dimensions or frame count exceed limits")
	}
	animation, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("decode GIF: %w", err)
	}
	middle := len(animation.Image) / 2
	canvas := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	var restore *image.RGBA
	var previousBounds image.Rectangle
	var previousDisposal byte
	for index, frame := range animation.Image[:middle+1] {
		if index > 0 {
			switch previousDisposal {
			case gif.DisposalBackground:
				draw.Draw(canvas, previousBounds, image.NewUniform(color.White), image.Point{}, draw.Src)
			case gif.DisposalPrevious:
				if restore != nil {
					draw.Draw(canvas, canvas.Bounds(), restore, image.Point{}, draw.Src)
				}
			}
		}
		var saved *image.RGBA
		if garminGIFDisposal(animation, index) == gif.DisposalPrevious {
			saved = cloneGarminGIFFrame(canvas)
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		restore = saved
		previousBounds = frame.Bounds()
		previousDisposal = garminGIFDisposal(animation, index)
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, canvas, &jpeg.Options{Quality: 85}); err != nil {
		return "", fmt.Errorf("encode GIF middle frame: %w", err)
	}
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes()), nil
}

func countGarminGIFFrames(data []byte) (int, error) {
	if len(data) < 13 || (string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a") {
		return 0, fmt.Errorf("invalid GIF header")
	}
	position := 13
	if data[10]&0x80 != 0 {
		position += 3 * (1 << ((data[10] & 7) + 1))
	}
	frames := 0
	for position < len(data) {
		switch data[position] {
		case 0x3b:
			return frames, nil
		case 0x21:
			position += 2
			var err error
			position, err = skipGarminGIFBlocks(data, position)
			if err != nil {
				return 0, err
			}
		case 0x2c:
			if position+10 > len(data) {
				return 0, fmt.Errorf("truncated GIF image descriptor")
			}
			frames++
			packed := data[position+9]
			position += 10
			if packed&0x80 != 0 {
				position += 3 * (1 << ((packed & 7) + 1))
			}
			if position >= len(data) {
				return 0, fmt.Errorf("truncated GIF image data")
			}
			position++
			var err error
			position, err = skipGarminGIFBlocks(data, position)
			if err != nil {
				return 0, err
			}
		default:
			return 0, fmt.Errorf("invalid GIF block at byte %d", position)
		}
	}
	return 0, fmt.Errorf("GIF has no trailer")
}

func skipGarminGIFBlocks(data []byte, position int) (int, error) {
	for position < len(data) {
		size := int(data[position])
		position++
		if size == 0 {
			return position, nil
		}
		if position+size > len(data) {
			return 0, fmt.Errorf("truncated GIF data block")
		}
		position += size
	}
	return 0, fmt.Errorf("truncated GIF data blocks")
}

func garminGIFDisposal(animation *gif.GIF, index int) byte {
	if index < len(animation.Disposal) {
		return animation.Disposal[index]
	}
	return gif.DisposalNone
}

func cloneGarminGIFFrame(source *image.RGBA) *image.RGBA {
	clone := image.NewRGBA(source.Bounds())
	copy(clone.Pix, source.Pix)
	return clone
}
