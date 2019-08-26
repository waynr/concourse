package skyserver

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/concourse/concourse/atc/db"

	"code.cloudfoundry.org/lager"
	"github.com/concourse/concourse/skymarshal/token"
	"github.com/coreos/go-oidc"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"gopkg.in/square/go-jose.v2/jwt"
)

type SkyConfig struct {
	Logger          lager.Logger
	TokenVerifier   token.Verifier
	TokenIssuer     token.Issuer
	TokenMiddleware token.Middleware
	UserFactory     db.UserFactory
	SigningKey      *rsa.PrivateKey
	SecureCookies   bool
	DexClientID     string
	DexClientSecret string
	DexRedirectURL  string
	DexIssuerURL    string
	DexHTTPClient   *http.Client
}

const stateCookieName = "skymarshal_state"

func NewSkyHandler(server *SkyServer) http.Handler {
	handler := http.NewServeMux()
	handler.HandleFunc("/sky/login", server.Login)
	handler.HandleFunc("/sky/logout", server.Logout)
	handler.HandleFunc("/sky/callback", server.Callback)
	handler.HandleFunc("/sky/userinfo", server.UserInfo)
	handler.HandleFunc("/sky/token", server.Token)
	return handler
}

func NewSkyServer(config *SkyConfig) (*SkyServer, error) {
	return &SkyServer{config}, nil
}

type SkyServer struct {
	config *SkyConfig
}

func (s *SkyServer) Login(w http.ResponseWriter, r *http.Request) {

	logger := s.config.Logger.Session("login")

	tokenString := s.config.TokenMiddleware.GetToken(r)
	if tokenString == "" {
		s.NewLogin(w, r)
		return
	}

	redirectURI := r.FormValue("redirect_uri")
	if redirectURI == "" {
		redirectURI = "/"
	}

	parts := strings.Split(tokenString, " ")

	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		logger.Info("failed-to-parse-cookie")
		s.NewLogin(w, r)
		return
	}

	parsed, err := jwt.ParseSigned(parts[1])
	if err != nil {
		logger.Error("failed-to-parse-cookie-token", err)
		s.NewLogin(w, r)
		return
	}

	var claims jwt.Claims
	var result map[string]interface{}

	if err = parsed.UnsafeClaimsWithoutVerification(&claims, &result); err != nil {
		logger.Error("failed-to-parse-claims", err)
		s.NewLogin(w, r)
		return
	}

	if err = claims.Validate(jwt.Expected{Time: time.Now()}); err != nil {
		logger.Error("failed-to-validate-claims", err)
		s.NewLogin(w, r)
		return
	}

	oauth2Token := &oauth2.Token{
		TokenType:   parts[0],
		AccessToken: parts[1],
		Expiry:      claims.Expiry.Time(),
	}

	token := oauth2Token.WithExtra(map[string]interface{}{
		"csrf": result["csrf"],
	})

	s.Redirect(w, r, token, redirectURI)
}

func (s *SkyServer) NewLogin(w http.ResponseWriter, r *http.Request) {

	redirectURI := r.FormValue("redirect_uri")
	if redirectURI == "" {
		redirectURI = "/"
	}

	oauth2Config := &oauth2.Config{
		ClientID:     s.config.DexClientID,
		ClientSecret: s.config.DexClientSecret,
		RedirectURL:  s.config.DexRedirectURL,
		Endpoint:     s.endpoint(),
		Scopes:       []string{"openid", "profile", "email", "federated:id", "groups"},
	}

	stateToken := encode(&token.StateToken{
		RedirectURI: redirectURI,
		Entropy:     token.RandomString(),
	})

	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    stateToken,
		Path:     "/",
		Expires:  time.Now().Add(time.Hour),
		Secure:   s.config.SecureCookies,
		HttpOnly: true,
	})

	authCodeURL := oauth2Config.AuthCodeURL(stateToken, oauth2.AccessTypeOffline)

	http.Redirect(w, r, authCodeURL, http.StatusTemporaryRedirect)
}

func (s *SkyServer) Callback(w http.ResponseWriter, r *http.Request) {

	logger := s.config.Logger.Session("callback")

	var (
		err                  error
		stateToken, authCode string
		dexToken             *oauth2.Token
		verifiedClaims       *token.VerifiedClaims
	)

	oauth2Config := &oauth2.Config{
		ClientID:     s.config.DexClientID,
		ClientSecret: s.config.DexClientSecret,
		RedirectURL:  s.config.DexRedirectURL,
		Endpoint:     s.endpoint(),
	}

	cookieState, err := r.Cookie(stateCookieName)
	if err != nil {
		logger.Error("failed-to-fetch-cookie-state", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if errMsg, errDesc := r.FormValue("error"), r.FormValue("error_description"); errMsg != "" {
		logger.Error("failed-with-callback-error", errors.New(errMsg+" : "+errDesc))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if stateToken = cookieState.Value; stateToken != r.FormValue("state") {
		logger.Error("failed-with-unexpected-state-token", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Path:     "/",
		MaxAge:   -1,
		Secure:   s.config.SecureCookies,
		HttpOnly: true,
	})

	if authCode = r.FormValue("code"); authCode == "" {
		logger.Error("failed-to-get-auth-code", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	ctx := oidc.ClientContext(r.Context(), s.config.DexHTTPClient)

	if dexToken, err = oauth2Config.Exchange(ctx, authCode); err != nil {
		logger.Error("failed-to-fetch-dex-token", err)
		switch e := err.(type) {
		case *oauth2.RetrieveError:
			http.Error(w, string(e.Body), e.Response.StatusCode)
			return
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}

	if verifiedClaims, err = s.config.TokenVerifier.Verify(ctx, dexToken); err != nil {
		logger.Error("failed-to-verify-dex-token", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if _, err = s.config.UserFactory.CreateOrUpdateUser(verifiedClaims.UserName, verifiedClaims.ConnectorID, verifiedClaims.Sub); err != nil {
		logger.Error("failed-to-save-user-to-database", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.Redirect(w, r, dexToken, decode(stateToken).RedirectURI)
}

func (s *SkyServer) Redirect(w http.ResponseWriter, r *http.Request, token *oauth2.Token, redirectURI string) {
	logger := s.config.Logger.Session("redirect")

	redirectURL, err := url.Parse(redirectURI)
	if err != nil {
		logger.Error("failed-to-parse-redirect-url", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	csrfToken, ok := token.Extra("csrf").(string)
	if !ok {
		logger.Info("failed-to-extract-csrf-token")
	}

	err = s.config.TokenMiddleware.SetToken(w, token.TokenType+" "+token.AccessToken, token.Expiry)
	if err != nil {
		logger.Error("invalid-token", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if redirectURL.Host != "" {
		logger.Error("invalid-redirect", fmt.Errorf("Unsupported redirect uri: %s", redirectURI))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	params := redirectURL.Query()
	params.Set("csrf_token", csrfToken)
	redirectURL.RawQuery = params.Encode()

	http.Redirect(w, r, redirectURL.String(), http.StatusTemporaryRedirect)
}

func (s *SkyServer) Token(w http.ResponseWriter, r *http.Request) {

	logger := s.config.Logger.Session("token")

	var (
		err            error
		dexToken       *oauth2.Token
		verifiedClaims *token.VerifiedClaims
	)

	if r.Method != "POST" {
		logger.Error("invalid-method", nil)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	clientID, clientSecret, ok := r.BasicAuth()
	if !ok {
		logger.Error("invalid-basic-auth", nil)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	grantType := r.FormValue("grant_type")
	username := r.FormValue("username")
	password := r.FormValue("password")
	scope := r.FormValue("scope")

	ctx := oidc.ClientContext(r.Context(), s.config.DexHTTPClient)

	if grantType == "client_credentials" {
		authConfig := clientcredentials.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			TokenURL:     s.endpoint().TokenURL,
			Scopes:       strings.Split(scope, "+"),
		}

		dexToken, err = authConfig.Token(ctx)
	}

	if grantType == "password" {
		oauth2Config := &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			Endpoint:     s.endpoint(),
			Scopes:       strings.Split(scope, "+"),
		}

		dexToken, err = oauth2Config.PasswordCredentialsToken(ctx, username, password)
	}

	if err != nil {
		logger.Error("failed-to-fetch-dex-token", err)
		switch e := err.(type) {
		case *oauth2.RetrieveError:
			http.Error(w, string(e.Body), e.Response.StatusCode)
			return
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}

	if verifiedClaims, err = s.config.TokenVerifier.Verify(ctx, dexToken); err != nil {
		logger.Error("failed-to-verify-dex-token", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if _, err = s.config.UserFactory.CreateOrUpdateUser(verifiedClaims.UserName, verifiedClaims.ConnectorID, verifiedClaims.Sub); err != nil {
		logger.Error("failed-to-save-user-to-database", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Add("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dexToken)
}

func (s *SkyServer) Logout(w http.ResponseWriter, r *http.Request) {
	s.config.TokenMiddleware.UnsetToken(w)
}

func (s *SkyServer) UserInfo(w http.ResponseWriter, r *http.Request) {

	logger := s.config.Logger.Session("userinfo")

	parts := strings.Split(r.Header.Get("Authorization"), " ")

	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	parsed, err := jwt.ParseSigned(parts[1])
	if err != nil {
		logger.Error("failed-to-parse-authorization-token", err)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var claims jwt.Claims
	var userInfo UserInfo

	if err = parsed.UnsafeClaimsWithoutVerification(&claims, &userInfo); err != nil {
		logger.Error("failed-to-parse-claims", err)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if err = claims.Validate(jwt.Expected{Time: time.Now()}); err != nil {
		logger.Error("failed-to-validate-claims", err)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	w.Header().Add("Content-Type", "application/json")

	json.NewEncoder(w).Encode(userInfo)
}

func (s *SkyServer) endpoint() oauth2.Endpoint {
	return oauth2.Endpoint{
		AuthURL:   strings.TrimRight(s.config.DexIssuerURL, "/") + "/auth",
		TokenURL:  strings.TrimRight(s.config.DexIssuerURL, "/") + "/token",
		AuthStyle: oauth2.AuthStyleInHeader,
	}
}

func encode(token *token.StateToken) string {
	json, _ := json.Marshal(token)

	return base64.StdEncoding.EncodeToString(json)
}

func decode(raw string) *token.StateToken {
	data, _ := base64.StdEncoding.DecodeString(raw)

	var token *token.StateToken
	json.Unmarshal(data, &token)
	return token
}

type UserInfo map[string]interface{}
