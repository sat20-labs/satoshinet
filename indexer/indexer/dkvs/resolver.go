package dkvs

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/sat20-labs/satoshinet/chaincfg"
)

type StaticDIDResolver struct {
	Names    map[string]DIDIdentity
	Services map[string]DIDIdentity
}

func (r StaticDIDResolver) ResolveName(name string) (DIDIdentity, error) {
	identity, ok := r.Names[name]
	if !ok {
		return DIDIdentity{}, ErrDIDResolverUnavailable
	}
	return identity, nil
}

func (r StaticDIDResolver) ResolveService(serviceName string) (DIDIdentity, error) {
	identity, ok := r.Services[serviceName]
	if !ok {
		return DIDIdentity{}, ErrDIDResolverUnavailable
	}
	return identity, nil
}

type StaticSystemVerifier struct {
	Keys [][]byte
}

func (v StaticSystemVerifier) CanWriteSystem(_ string, pubKey []byte) error {
	return DIDIdentity{Active: true, SigningKeys: v.Keys}.CanSign(pubKey)
}

type HTTPSystemVerifier struct {
	Endpoint string
	Client   *http.Client
}

type httpSystemVerifyRequest struct {
	Key          string `json:"key"`
	PubKeyHex    string `json:"pubkey_hex"`
	PubKeyBase64 string `json:"pubkey_base64"`
}

type httpSystemVerifyResponse struct {
	Code  int                       `json:"code,omitempty"`
	Msg   string                    `json:"msg,omitempty"`
	Valid *bool                     `json:"valid,omitempty"`
	Data  *httpSystemVerifyResponse `json:"data,omitempty"`
}

func (v HTTPSystemVerifier) CanWriteSystem(key string, pubKey []byte) error {
	endpoint := strings.TrimSpace(v.Endpoint)
	if endpoint == "" {
		return ErrPermissionDenied
	}
	req := httpSystemVerifyRequest{
		Key:          key,
		PubKeyHex:    hex.EncodeToString(pubKey),
		PubKeyBase64: base64.StdEncoding.EncodeToString(pubKey),
	}
	encoded, err := json.Marshal(req)
	if err != nil {
		return err
	}
	client := v.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New(resp.Status)
	}
	var verifyResp httpSystemVerifyResponse
	if err := json.NewDecoder(resp.Body).Decode(&verifyResp); err != nil {
		return err
	}
	return verifyResp.err()
}

func (r httpSystemVerifyResponse) err() error {
	if r.Code != 0 {
		if r.Msg != "" {
			return errors.New(r.Msg)
		}
		return ErrPermissionDenied
	}
	src := &r
	if r.Data != nil {
		src = r.Data
	}
	if src.Valid != nil && *src.Valid {
		return nil
	}
	if src.Msg != "" {
		return errors.New(src.Msg)
	}
	return ErrPermissionDenied
}

type HTTPDIDResolver struct {
	BaseURL     string
	NamePath    string
	ServicePath string
	Client      *http.Client
}

func (r HTTPDIDResolver) ResolveName(name string) (DIDIdentity, error) {
	return r.resolve(r.path("name"), name)
}

func (r HTTPDIDResolver) ResolveService(serviceName string) (DIDIdentity, error) {
	return r.resolve(r.path("service"), serviceName)
}

func (r HTTPDIDResolver) path(kind string) string {
	switch kind {
	case "service":
		if r.ServicePath != "" {
			return r.ServicePath
		}
		return "/service/"
	default:
		if r.NamePath != "" {
			return r.NamePath
		}
		return "/name/"
	}
}

func (r HTTPDIDResolver) resolve(path, name string) (DIDIdentity, error) {
	base := strings.TrimRight(r.BaseURL, "/")
	if base == "" || strings.TrimSpace(name) == "" {
		return DIDIdentity{}, ErrDIDResolverUnavailable
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := base + "/" + strings.Trim(strings.TrimSpace(path), "/") + "/" + url.PathEscape(name)
	resp, err := client.Get(endpoint)
	if err != nil {
		return DIDIdentity{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return DIDIdentity{}, ErrDIDResolverUnavailable
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return DIDIdentity{}, errors.New(resp.Status)
	}
	var payload httpDIDResolverResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return DIDIdentity{}, err
	}
	identity, err := payload.identity(name)
	if err != nil {
		return DIDIdentity{}, err
	}
	return identity, nil
}

type httpDIDResolverResponse struct {
	Code           int                      `json:"code,omitempty"`
	Msg            string                   `json:"msg,omitempty"`
	Data           *httpDIDResolverIdentity `json:"data,omitempty"`
	CanonicalName  string                   `json:"canonical_name,omitempty"`
	NameID         string                   `json:"name_id,omitempty"`
	SigningKeys    []string                 `json:"signing_keys,omitempty"`
	OwnerAddresses []string                 `json:"owner_addresses,omitempty"`
	Address        string                   `json:"address,omitempty"`
	Active         *bool                    `json:"active,omitempty"`
}

type httpDIDResolverIdentity struct {
	CanonicalName  string   `json:"canonical_name,omitempty"`
	NameID         string   `json:"name_id,omitempty"`
	SigningKeys    []string `json:"signing_keys,omitempty"`
	OwnerAddresses []string `json:"owner_addresses,omitempty"`
	Address        string   `json:"address,omitempty"`
	Active         *bool    `json:"active,omitempty"`
}

func (r httpDIDResolverResponse) identity(requestName string) (DIDIdentity, error) {
	if r.Code != 0 {
		if r.Msg != "" {
			return DIDIdentity{}, errors.New(r.Msg)
		}
		return DIDIdentity{}, ErrDIDResolverUnavailable
	}
	src := httpDIDResolverIdentity{
		CanonicalName:  r.CanonicalName,
		NameID:         r.NameID,
		SigningKeys:    r.SigningKeys,
		OwnerAddresses: r.OwnerAddresses,
		Address:        r.Address,
		Active:         r.Active,
	}
	if r.Data != nil {
		src = *r.Data
	}
	active := false
	if src.Active != nil {
		active = *src.Active
	}
	canonicalName := src.CanonicalName
	if canonicalName == "" {
		canonicalName = requestName
	}
	nameID := src.NameID
	if nameID == "" {
		nameID = NormalizeNameID(canonicalName)
	}
	keys, err := decodeSigningKeys(src.SigningKeys)
	if err != nil {
		return DIDIdentity{}, err
	}
	ownerAddresses := normalizeOwnerAddresses(src.OwnerAddresses, src.Address)
	return DIDIdentity{
		CanonicalName:  canonicalName,
		NameID:         nameID,
		SigningKeys:    keys,
		OwnerAddresses: ownerAddresses,
		Active:         active,
	}, nil
}

type L1NSResolver struct {
	BaseURL       string
	NamePath      string
	ServicePath   string
	AddressParams *chaincfg.Params
	Client        *http.Client
}

func (r L1NSResolver) ResolveName(name string) (DIDIdentity, error) {
	return r.resolve(r.path("name"), name)
}

func (r L1NSResolver) ResolveService(serviceName string) (DIDIdentity, error) {
	return r.resolve(r.path("service"), serviceName)
}

func (r L1NSResolver) path(kind string) string {
	switch kind {
	case "service":
		if r.ServicePath != "" {
			return r.ServicePath
		}
		return "/ns/name/"
	default:
		if r.NamePath != "" {
			return r.NamePath
		}
		return "/ns/name/"
	}
}

func (r L1NSResolver) resolve(path, name string) (DIDIdentity, error) {
	base := strings.TrimRight(r.BaseURL, "/")
	if base == "" || strings.TrimSpace(name) == "" {
		return DIDIdentity{}, ErrDIDResolverUnavailable
	}
	client := r.Client
	if client == nil {
		client = http.DefaultClient
	}
	endpoint := base + "/" + strings.Trim(strings.TrimSpace(path), "/") + "/" + url.PathEscape(name)
	resp, err := client.Get(endpoint)
	if err != nil {
		return DIDIdentity{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return DIDIdentity{}, ErrDIDResolverUnavailable
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return DIDIdentity{}, errors.New(resp.Status)
	}
	var payload l1NSNameResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return DIDIdentity{}, err
	}
	return payload.identity(name, r.AddressParams)
}

type l1NSNameResponse struct {
	Code int           `json:"code,omitempty"`
	Msg  string        `json:"msg,omitempty"`
	Data *l1NSNameData `json:"data,omitempty"`
}

type l1NSNameData struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address,omitempty"`
}

func (r l1NSNameResponse) identity(requestName string, params *chaincfg.Params) (DIDIdentity, error) {
	if r.Code != 0 {
		if r.Msg != "" {
			return DIDIdentity{}, errors.New(r.Msg)
		}
		return DIDIdentity{}, ErrDIDResolverUnavailable
	}
	if r.Data == nil || strings.TrimSpace(r.Data.Address) == "" {
		return DIDIdentity{}, ErrDIDResolverUnavailable
	}
	canonicalName := strings.TrimSpace(r.Data.Name)
	if canonicalName == "" {
		canonicalName = requestName
	}
	return DIDIdentity{
		CanonicalName:  canonicalName,
		NameID:         NormalizeNameID(canonicalName),
		OwnerAddresses: normalizeOwnerAddresses(nil, r.Data.Address),
		AddressParams:  params,
		Active:         true,
	}, nil
}

func decodeSigningKeys(encoded []string) ([][]byte, error) {
	keys := make([][]byte, 0, len(encoded))
	for _, item := range encoded {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key, err := hex.DecodeString(item)
		if err != nil {
			key, err = base64.StdEncoding.DecodeString(item)
			if err != nil {
				return nil, err
			}
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func normalizeOwnerAddresses(addresses []string, single string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(addresses)+1)
	appendAddress := func(address string) {
		address = strings.TrimSpace(address)
		if address == "" {
			return
		}
		if _, ok := seen[address]; ok {
			return
		}
		seen[address] = struct{}{}
		out = append(out, address)
	}
	for _, address := range addresses {
		appendAddress(address)
	}
	appendAddress(single)
	return out
}
