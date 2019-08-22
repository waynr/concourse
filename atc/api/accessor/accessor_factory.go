package accessor

import (
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	jwt "github.com/dgrijalva/jwt-go"
	"gopkg.in/square/go-jose.v2"
)

//go:generate counterfeiter . AccessFactory

type AccessFactory interface {
	Create(*http.Request, string) Access
}

type accessFactory struct {
	target    *url.URL
	publicKey *rsa.PublicKey
}

func NewAccessFactory(target *url.URL, key *rsa.PublicKey) AccessFactory {
	factory := &accessFactory{
		target:    target,
		publicKey: key,
	}

	go factory.tick(time.Minute)

	return factory
}

func (a *accessFactory) Create(r *http.Request, action string) Access {

	header := r.Header.Get("Authorization")
	if header == "" {
		return &access{nil, action}
	}

	fmt.Println("^^^^^^^^^^^^^^^^^^^^^^^^^^^ header", header)

	if len(header) < 7 || strings.ToUpper(header[0:6]) != "BEARER" {
		return &access{&jwt.Token{}, action}
	}

	fmt.Println("^^^^^^^^^^^^^^^^^^^^^^^^^^^ pk", a.publicKey)

	token, err := jwt.Parse(header[7:], a.validate)
	if err != nil {
		return &access{&jwt.Token{}, action}
	}

	fmt.Println("^^^^^^^^^^^^^^^^^^^^^^^^^^^ token", token)
	return &access{token, action}
}

func (a *accessFactory) validate(token *jwt.Token) (interface{}, error) {

	if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
		return nil, fmt.Errorf("Unexpected signing method: %v", token.Header["alg"])
	}

	fmt.Println("+++++++++++++++++++++++++++ validate", a.publicKey)
	return a.publicKey, nil
}

func (a *accessFactory) tick(interval time.Duration) {

	if err := a.refresh(); err != nil {
		fmt.Println("+++++++++++++++++++++++++++", err)
	}

	for range time.Tick(interval) {
		if err := a.refresh(); err != nil {
			fmt.Println("+++++++++++++++++++++++++++", err)
		}

	}
}

func (a *accessFactory) refresh() error {

	key, err := a.fetch()
	if err != nil {
		return err
	}

	a.publicKey = key
	return nil
}

func (a *accessFactory) fetch() (*rsa.PublicKey, error) {

	token, retry, err := a.attempt()

	for retry {
		time.Sleep(time.Second)
		token, retry, err = a.attempt()
	}

	return token, err
}

func (a *accessFactory) attempt() (*rsa.PublicKey, bool, error) {

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

	var data struct {
		Keys []jose.JSONWebKey `json:"keys"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, false, err
	}

	return data.Keys[0].Public().Key.(*rsa.PublicKey), false, nil
}
