package dkvs

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
