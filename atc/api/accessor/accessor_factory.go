package accessor

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/concourse/concourse/atc/db"
	jwt "github.com/dgrijalva/jwt-go"
	"gopkg.in/square/go-jose.v2"
)

//go:generate counterfeiter . AccessFactory

type AccessFactory interface {
	Create(*http.Request, string) Access
}

type accessFactory struct {
	sync.Mutex
	target      *url.URL
	publicKey   *rsa.PublicKey
	teamFactory db.TeamFactory
}

func NewAccessFactory(target *url.URL, key *rsa.PublicKey, teamFactory db.TeamFactory) AccessFactory {
	factory := &accessFactory{
		target:      target,
		publicKey:   key,
		teamFactory: teamFactory,
	}

	return factory
}

func (a *accessFactory) Create(r *http.Request, action string) Access {

	header := r.Header.Get("Authorization")
	if header == "" {
		return &access{nil, action, a.teamFactory}
	}

	if len(header) < 7 || strings.ToUpper(header[0:6]) != "BEARER" {
		return &access{&jwt.Token{}, action, a.teamFactory}
	}

	token, err := jwt.Parse(header[7:], a.validate)
	if err != nil {

		err = a.refreshPublicKey()
		if err != nil {
			return &access{&jwt.Token{}, action, a.teamFactory}
		}

		token, err = jwt.Parse(header[7:], a.validate)
		if err != nil {
			return &access{&jwt.Token{}, action, a.teamFactory}
		}
	}

	return &access{token, action, a.teamFactory}
}

func (a *accessFactory) validate(token *jwt.Token) (interface{}, error) {

	if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
		return nil, fmt.Errorf("Unexpected signing method: %v", token.Header["alg"])
	}

	return a.publicKey, nil
}

func (a *accessFactory) refreshPublicKey() error {

	key, err := a.fetchPublicKey()
	if err != nil {
		return err
	}

	a.Lock()
	*a.publicKey = *key
	a.Unlock()

	return nil
}

func (a *accessFactory) fetchPublicKey() (*rsa.PublicKey, error) {

	token, retry, err := a.tryFetchPublicKey()

	for retry {
		time.Sleep(time.Second)
		token, retry, err = a.tryFetchPublicKey()
	}

	return token, err
}

func (a *accessFactory) tryFetchPublicKey() (*rsa.PublicKey, bool, error) {

	resp, err := http.Get(a.target.String())
	if err != nil {
		return nil, true, err
	}

	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 500:
		return nil, true, fmt.Errorf("server error: %v", resp.StatusCode)

	case resp.StatusCode >= 400:
		return nil, false, fmt.Errorf("client error: %v", resp.StatusCode)
	}

	var data jose.JSONWebKeySet
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, false, err
	}

	if len(data.Keys) > 0 {
		return data.Keys[0].Public().Key.(*rsa.PublicKey), false, nil
	} else {
		return nil, false, errors.New("no keys found")
	}
}
