package main

import (
	"fmt"
	"image"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/net/html"
)

type LinkInfo struct {
	Text string
	Href string
	Line int
}

type ImageInfo struct {
	Src  string
	Alt  string
}

var asciiChars = []string{" ", ".", ":", "-", "=", "+", "*", "#", "%", "@"}
var downloadDir = "downloads"

func init() {
	os.MkdirAll(downloadDir, 0755)
	rand.Seed(time.Now().UnixNano())
}

func cleanupDownloads() {
	files, err := filepath.Glob(filepath.Join(downloadDir, "*"))
	if err != nil {
		fmt.Printf("Error finding download files: %v\n", err)
		return
	}

	for _, file := range files {
		if err := os.Remove(file); err != nil {
			fmt.Printf("Error removing file %s: %v\n", file, err)
		}
	}
}

func generateUniqueFilename(ext string) string {
	timestamp := time.Now().UnixNano()
	randomSuffix := rand.Intn(10000)
	return filepath.Join(downloadDir, fmt.Sprintf("img_%d_%d%s", timestamp, randomSuffix, ext))
}

func fetchURL(inputURL string) (string, error) {
	parsedURL, err := url.Parse(inputURL)
	if err != nil {
		return "", fmt.Errorf("error parsing URL: %v", err)
	}

	if parsedURL.Scheme == "" {
		parsedURL.Scheme = "https"
	}

	resp, err := http.Get(parsedURL.String())
	if err != nil {
		return "", fmt.Errorf("error fetching URL: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("bad status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response body: %v", err)
	}

	return string(body), nil
}

func resolveURL(baseURL, linkHref string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return linkHref
	}

	link, err := url.Parse(linkHref)
	if err != nil {
		return linkHref
	}

	resolvedURL := base.ResolveReference(link)
	return resolvedURL.String()
}

func downloadImage(imageURL string) (string, error) {
	resp, err := http.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("error downloading image: %v", err)
	}
	defer resp.Body.Close()

	ext := filepath.Ext(imageURL)
	if ext == "" {
		ext = ".jpg"
	}

	filename := generateUniqueFilename(ext)

	out, err := os.Create(filename)
	if err != nil {
		return "", fmt.Errorf("error creating file: %v", err)
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return "", fmt.Errorf("error saving image: %v", err)
	}

	return filename, nil
}

func imageToASCII(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", fmt.Errorf("error opening image: %v", err)
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return "", fmt.Errorf("error decoding image: %v", err)
	}

	bounds := img.Bounds()
	width := 80
	height := width * bounds.Dy() / bounds.Dx()

	var ascii strings.Builder
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			origX := x * bounds.Dx() / width
			origY := y * bounds.Dy() / height
			
			c := img.At(origX, origY)
			r, g, b, _ := c.RGBA()
			brightness := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 65535.0
			charIndex := int(brightness * float64(len(asciiChars)-1))
			ascii.WriteString(asciiChars[charIndex])
		}
		ascii.WriteString("\n")
	}

	return ascii.String(), nil
}

func extractContent(node *html.Node, currentURL string) (string, []LinkInfo, []ImageInfo) {
	var text string
	var links []LinkInfo
	var images []ImageInfo
	var lineCount int

	var extractFunc func(*html.Node, int) (string, []LinkInfo, []ImageInfo)
	extractFunc = func(n *html.Node, currentLine int) (string, []LinkInfo, []ImageInfo) {
		var extractedText string
		var extractedLinks []LinkInfo
		var extractedImages []ImageInfo

		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style") {
			return "", nil, nil
		}

		if n.Type == html.ElementNode && n.Data == "a" {
			linkText := ""
			linkHref := ""
			for _, attr := range n.Attr {
				if attr.Key == "href" {
					linkHref = attr.Val
				}
			}
			
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				childText, _, _ := extractFunc(c, currentLine)
				linkText += childText
			}
			
			linkText = strings.TrimSpace(linkText)
			if linkText != "" && linkHref != "" {
				resolvedLink := resolveURL(currentURL, linkHref)
				extractedLinks = append(extractedLinks, LinkInfo{
					Text: linkText, 
					Href: resolvedLink, 
					Line: currentLine,
				})
				return linkText + " ", extractedLinks, extractedImages
			}
		}

		if n.Type == html.ElementNode && n.Data == "img" {
			var src, alt string
			for _, attr := range n.Attr {
				switch attr.Key {
				case "src":
					src = attr.Val
				case "alt":
					alt = attr.Val
				}
			}
			
			if src != "" {
				resolvedSrc := resolveURL(currentURL, src)
				extractedImages = append(extractedImages, ImageInfo{Src: resolvedSrc, Alt: alt})
				return alt + " ", extractedLinks, extractedImages
			}
		}

		if n.Type == html.TextNode {
			extractedText = strings.TrimSpace(n.Data)
			if extractedText != "" {
				lineCount++
				return extractedText + "\n", nil, nil
			}
			return "", nil, nil
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			childText, childLinks, childImages := extractFunc(c, lineCount)
			extractedText += childText
			extractedLinks = append(extractedLinks, childLinks...)
			extractedImages = append(extractedImages, childImages...)
		}

		return extractedText, extractedLinks, extractedImages
	}

	text, links, images = extractFunc(node, 0)
	return text, links, images
}

func renderHTML(htmlContent, currentURL string) (string, []LinkInfo, []ImageInfo, error) {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		return "", nil, nil, fmt.Errorf("error parsing HTML: %v", err)
	}

	var bodyText string
	var bodyLinks []LinkInfo
	var bodyImages []ImageInfo
	var findBody func(*html.Node)
	findBody = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "body" {
			bodyText, bodyLinks, bodyImages = extractContent(n, currentURL)
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			findBody(c)
		}
	}
	findBody(doc)

	return bodyText, bodyLinks, bodyImages, nil
}

func browseInteractive(initialURL string) error {
	app := tview.NewApplication()
	textView := tview.NewTextView().
		SetDynamicColors(true).
		SetRegions(true).
		SetWordWrap(true)
	
	var currentURL string
	var links []LinkInfo

	textView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			app.Stop()
			return nil
		}
		return event
	})

	textView.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
		if action == tview.MouseLeftClick {
			_, y := event.Position()
			
			// Adjust for text view's internal scrolling
			_, scrollOffset := textView.GetScrollOffset()
			y += scrollOffset

			// Check if click is on a link
			for _, link := range links {
				if link.Line == y {
					currentURL = link.Href
					go func() {
						htmlContent, err := fetchURL(currentURL)
						if err != nil {
							textView.SetText(fmt.Sprintf("Error fetching URL: %v", err))
							return
						}

						renderedText, newLinks, _, err := renderHTML(htmlContent, currentURL)
						if err != nil {
							textView.SetText(fmt.Sprintf("Error rendering HTML: %v", err))
							return
						}

						app.QueueUpdateDraw(func() {
							textView.SetText(renderedText)
							links = newLinks
						})
					}()
					break
				}
			}
		}
		return action, event
	})

	// Initial page load
	go func() {
		currentURL = initialURL
		htmlContent, err := fetchURL(currentURL)
		if err != nil {
			textView.SetText(fmt.Sprintf("Error fetching URL: %v", err))
			return
		}

		renderedText, newLinks, _, err := renderHTML(htmlContent, currentURL)
		if err != nil {
			textView.SetText(fmt.Sprintf("Error rendering HTML: %v", err))
			return
		}

		app.QueueUpdateDraw(func() {
			textView.SetText(renderedText)
			links = newLinks
		})
	}()

	if err := app.SetRoot(textView, true).EnableMouse(true).Run(); err != nil {
		return err
	}

	return nil
}

func main() {
	defer cleanupDownloads()

	if len(os.Args) < 2 {
		fmt.Println("Usage: go run main.go <url>")
		os.Exit(1)
	}

	url := os.Args[1]
	
	err := browseInteractive(url)
	if err != nil {
		fmt.Printf("Error browsing: %v\n", err)
		os.Exit(1)
	}
}
