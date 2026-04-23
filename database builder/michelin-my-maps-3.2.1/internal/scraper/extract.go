package scraper

import (
	"strings"

	"github.com/gocolly/colly/v2"
	"github.com/ngshiheng/michelin-my-maps/v3/internal/parser"
	"github.com/ngshiheng/michelin-my-maps/v3/internal/storage"
	log "github.com/sirupsen/logrus"
)

// extractRestaurantData extracts restaurant data from the XML element.
func (s *Scraper) extractRestaurantData(e *colly.XMLElement) storage.RestaurantData {
	url := e.Request.URL.String()
	websiteUrl := e.ChildAttr(restaurantWebsiteUrlXPath, "href")
	name := e.ChildText(restaurantNameXPath)

	address := e.ChildText(restaurantAddressXPath)
	address = strings.ReplaceAll(address, "\n", " ")

	description := e.ChildText(restaurantDescriptionXPath)
	distinction := e.ChildText(restaurantDistinctionXPath)
	// The XPath now targets an element carrying data-green-star="true", so
	// read the attribute value directly. ChildText returned empty strings
	// after Michelin's 2026 template change and silently broke green-star
	// capture for the entire 2026 refresh.
	greenStar := e.ChildAttr(restaurantGreenStarXPath, "data-green-star")

	priceAndCuisine := e.ChildText(restaurantPriceAndCuisineXPath)
	price, cuisine := parser.SplitUnpack(priceAndCuisine, "·")

	phoneNumber := e.ChildAttr(restaurantPhoneNumberXPath, "href")
	formattedPhoneNumber := parser.ParsePhoneNumber(phoneNumber)
	if formattedPhoneNumber == "" {
		log.WithFields(log.Fields{
			"phone_number": phoneNumber,
			"url":          url,
		}).Debug("invalid phone number")
	}

	facilitiesAndServices := e.ChildTexts(restaurantFacilitiesAndServicesXPath)

	var year int
	if v := e.Request.Ctx.GetAny("publishedYear"); v != nil {
		if y, ok := v.(int); ok {
			year = y
		}
	}
	// Diff mode: the -year flag is authoritative. Override whatever the
	// detail page's JSON-LD reports so a 2027 differential run saves award
	// rows under 2027 even when Michelin hasn't rolled the guide over yet.
	if v := e.Request.Ctx.GetAny("diff_target_year"); v != nil {
		if y, ok := v.(int); ok && y > 0 {
			year = y
		}
	}

	inGuideTrue := true
	return storage.RestaurantData{
		URL:                   url,
		Year:                  year,
		Name:                  name,
		Address:               address,
		Location:              e.Request.Ctx.Get("location"),
		Latitude:              e.Request.Ctx.Get("latitude"),
		Longitude:             e.Request.Ctx.Get("longitude"),
		Cuisine:               cuisine,
		PhoneNumber:           formattedPhoneNumber,
		WebsiteURL:            websiteUrl,
		Distinction:           parser.ParseDistinction(distinction),
		Description:           parser.TrimWhiteSpaces(description),
		Price:                 price,
		FacilitiesAndServices: strings.Join(facilitiesAndServices, ","),
		GreenStar:             parser.ParseGreenStar(greenStar),
		WaybackURL:            "",
		InGuide:               &inGuideTrue, // Current scraped restaurants are in guide
	}
}
