package web

import (
	"net/http"
	"net/url"

	"code.cloudfoundry.org/lager"
	"github.com/concourse/concourse/web/indexhandler"
	"github.com/concourse/concourse/web/proxyhandler"
	"github.com/concourse/concourse/web/publichandler"
	"github.com/concourse/concourse/web/robotshandler"
)

func NewHandler(logger lager.Logger, apiURL *url.URL) (http.Handler, error) {
	indexHandler, err := indexhandler.NewHandler(logger)
	if err != nil {
		return nil, err
	}

	publicHandler, err := publichandler.NewHandler()
	if err != nil {
		return nil, err
	}

	proxyHandler, err := proxyhandler.NewHandler(logger, apiURL)
	if err != nil {
		return nil, err
	}

	robotsHandler := robotshandler.NewHandler()

	webMux := http.NewServeMux()
	webMux.Handle("/api/", proxyHandler)
	webMux.Handle("/public/", publicHandler)
	webMux.Handle("/robots.txt", robotsHandler)
	webMux.Handle("/", indexHandler)
	return webMux, nil
}
