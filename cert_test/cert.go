package cert_test

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"io"
	"net/netip"
	"time"

	"github.com/slackhq/nebula/cert"
	"github.com/slackhq/nebula/noiseutil"
	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/ed25519"
)

// NewTestCaCert will create a new ca certificate
func NewTestCaCert(version cert.Version, curve cert.Curve, before, after time.Time, networks, unsafeNetworks []netip.Prefix, groups []string, compressKey bool) (cert.Certificate, []byte, []byte, []byte) {
	var err error
	var pub, priv []byte

	switch curve {
	case cert.Curve_CURVE25519:
		pub, priv, err = ed25519.GenerateKey(rand.Reader)
	case cert.Curve_P256:
		privk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			panic(err)
		}
		if compressKey {
			pub = elliptic.MarshalCompressed(elliptic.P256(), privk.PublicKey.X, privk.PublicKey.Y)
		} else {
			pub = elliptic.Marshal(elliptic.P256(), privk.PublicKey.X, privk.PublicKey.Y)
		}
		priv = privk.D.FillBytes(make([]byte, 32))
	default:
		// There is no default to allow the underlying lib to respond with an error
	}

	if before.IsZero() {
		before = time.Now().Add(time.Second * -60).Round(time.Second)
	}
	if after.IsZero() {
		after = time.Now().Add(time.Second * 60).Round(time.Second)
	}

	t := &cert.TBSCertificate{
		Curve:          curve,
		Version:        version,
		Name:           "test ca",
		NotBefore:      time.Unix(before.Unix(), 0),
		NotAfter:       time.Unix(after.Unix(), 0),
		PublicKey:      pub,
		Networks:       networks,
		UnsafeNetworks: unsafeNetworks,
		Groups:         groups,
		IsCA:           true,
	}

	c, err := t.Sign(nil, curve, priv)
	if err != nil {
		panic(err)
	}

	pem, err := c.MarshalPEM()
	if err != nil {
		panic(err)
	}

	return c, pub, priv, pem
}

// NewTestCert will generate a signed certificate with the provided details.
// Expiry times are defaulted if you do not pass them in
func NewTestCert(v cert.Version, curve cert.Curve, ca cert.Certificate, key []byte, name string, before, after time.Time, networks, unsafeNetworks []netip.Prefix, groups []string, compressKey bool) (cert.Certificate, []byte, []byte, []byte) {
	if before.IsZero() {
		before = time.Now().Add(time.Second * -60).Round(time.Second)
	}

	if after.IsZero() {
		after = time.Now().Add(time.Second * 60).Round(time.Second)
	}

	var pub, priv []byte
	switch curve {
	case cert.Curve_CURVE25519:
		pub, priv = X25519Keypair()
	case cert.Curve_P256:
		pub, priv = P256Keypair(compressKey)
	default:
		panic("unknown curve")
	}

	nc := &cert.TBSCertificate{
		Version:        v,
		Curve:          curve,
		Name:           name,
		Networks:       networks,
		UnsafeNetworks: unsafeNetworks,
		Groups:         groups,
		NotBefore:      time.Unix(before.Unix(), 0),
		NotAfter:       time.Unix(after.Unix(), 0),
		PublicKey:      pub,
		IsCA:           false,
	}

	c, err := nc.Sign(ca, ca.Curve(), key)
	if err != nil {
		panic(err)
	}

	pem, err := c.MarshalPEM()
	if err != nil {
		panic(err)
	}

	return c, pub, cert.MarshalPrivateKeyToPEM(curve, priv), pem
}

func X25519Keypair() ([]byte, []byte) {
	privkey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, privkey); err != nil {
		panic(err)
	}

	pubkey, err := curve25519.X25519(privkey, curve25519.Basepoint)
	if err != nil {
		panic(err)
	}

	return pubkey, privkey
}

func P256Keypair(compressed bool) ([]byte, []byte) {
	privkey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	if !compressed {
		pubkey := privkey.PublicKey()
		return pubkey.Bytes(), privkey.Bytes()
	}
	pubkeyBytes := privkey.PublicKey().Bytes()
	pubkey, err := noiseutil.LoadP256Pubkey(pubkeyBytes)
	if err != nil {
		panic(err)
	}
	out := elliptic.MarshalCompressed(elliptic.P256(), pubkey.X, pubkey.Y)
	return out, privkey.Bytes()
}
