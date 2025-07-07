package main

import (
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

func main() {
	// URL of the webpage you want to scrape
	url := "https://guide.michelin.com/en/california/us-los-angeles/restaurant/osteria-mozza"

	// Make an HTTP request
	res, err := http.Get(url)
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()

	// Check for HTTP errors
	if res.StatusCode != 200 {
		log.Fatalf("Failed to fetch webpage: %d %s", res.StatusCode, res.Status)
	}

	// Load the HTML document
	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		log.Fatal(err)
	}

	// Try multiple selectors to find the correct image URL
	var imageURL string

	// Check meta tags first (most reliable for this site)
	doc.Find("meta[property='og:image']").Each(func(i int, s *goquery.Selection) {
		content, exists := s.Attr("content")
		if exists && strings.Contains(content, "axwwgrkdco.cloudimg.io") {
			imageURL = content
			return
		}
	})

	// If not found in meta tags, try looking in other places
	if imageURL == "" {
		// Try data attributes that typically hold the main restaurant image
		doc.Find("[data-ci-bg-url]").Each(func(i int, s *goquery.Selection) {
			src, exists := s.Attr("data-ci-bg-url")
			if exists && strings.Contains(src, "axwwgrkdco.cloudimg.io") {
				imageURL = src
				return
			}
		})
	}

	// If still not found, check any img tags
	if imageURL == "" {
		doc.Find("img").Each(func(i int, s *goquery.Selection) {
			src, exists := s.Attr("src")
			if exists && strings.Contains(src, "axwwgrkdco.cloudimg.io") {
				imageURL = src
				return
			}
		})
	}

	if imageURL == "" {
		log.Fatal("No image found with the specified URL prefix")
	}

	// Clean the URL (removing any query parameters if needed)
	if strings.Contains(imageURL, "?") {
		imageURL = strings.Split(imageURL, "?")[0]
	}

	// Print the URL of the first image with the specified prefix
	fmt.Println("The URL of the first image is:", imageURL)

	// Open the image URL in the default browser (macOS)
	fmt.Println("Opening image in browser...")
	cmd := exec.Command("open", imageURL)
	err = cmd.Run()
	if err != nil {
		log.Printf("Failed to open URL in browser: %v", err)
	} else {
		fmt.Println("Image opened successfully in browser")
	}
}
