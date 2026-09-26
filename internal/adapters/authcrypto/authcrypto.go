// Package authcrypto provides the login flow's cryptography: bcrypt
// password hashing, HS256 JWTs, and TOTP (RFC 6238) with QR codes.
package authcrypto

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image/png"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/codeStev/stl-library/internal/app"
	"github.com/codeStev/stl-library/internal/core/account"
)

// Bcrypt hashes with bcrypt.
type Bcrypt struct{ Cost int }

func (b Bcrypt) Hash(secret string) (string, error) {
	cost := b.Cost
	if cost == 0 {
		cost = 12
	}
	h, err := bcrypt.GenerateFromPassword([]byte(secret), cost)
	return string(h), err
}

func (Bcrypt) Compare(hash, secret string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret)) == nil
}

// JWT signs tokens with HS256.
type JWT struct {
	Key    []byte
	Issuer string
}

type claims struct {
	Role    string   `json:"role"`
	Version int      `json:"ver"`
	Factors []string `json:"factors"`
	jwt.RegisteredClaims
}

func (j JWT) Issue(c app.Claims) (string, error) {
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		Role: string(c.Role), Version: c.Version, Factors: c.Factors,
		RegisteredClaims: jwt.RegisteredClaims{Subject: c.Subject, ID: c.ID, Issuer: j.Issuer,
			IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Unix(c.Expires, 0))},
	})
	return t.SignedString(j.Key)
}

func (j JWT) Parse(token string) (app.Claims, error) {
	var c claims
	_, err := jwt.ParseWithClaims(token, &c, func(*jwt.Token) (any, error) { return j.Key, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(j.Issuer), jwt.WithExpirationRequired())
	if err != nil {
		return app.Claims{}, err
	}
	if c.Subject == "" || c.ID == "" || c.ExpiresAt == nil {
		return app.Claims{}, errors.New("incomplete token")
	}
	return app.Claims{Subject: c.Subject, Role: account.Role(c.Role), Version: c.Version, Factors: c.Factors,
		ID: c.ID, Expires: c.ExpiresAt.Unix()}, nil
}

// TOTP: SHA1, 6 digits, 30 seconds - what every authenticator app
// implements (stronger variants silently fail in many of them).
type TOTP struct{ Issuer string }

func (t TOTP) NewSecret(accountName string) (string, string, string, error) {
	key, err := totp.Generate(totp.GenerateOpts{Issuer: t.Issuer, AccountName: accountName, Algorithm: otp.AlgorithmSHA1, Digits: otp.DigitsSix, Period: 30})
	if err != nil {
		return "", "", "", err
	}
	img, err := key.Image(240, 240)
	if err != nil {
		return "", "", "", err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return "", "", "", err
	}
	return key.Secret(), key.URL(), "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// Validate accepts the current code and one step either side (clock skew).
func (TOTP) Validate(secret, code string, now time.Time) bool {
	ok, err := totp.ValidateCustom(code, secret, now, totp.ValidateOpts{Period: 30, Skew: 1, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	return err == nil && ok
}
