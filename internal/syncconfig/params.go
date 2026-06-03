package syncconfig

import (
	"github.com/gv/jitenv/internal/crypto"
)

// adapterParamAAD binds an adapter param value to its slot:
// "syncadapter.<name>.<key>". Mirrors config.SourceParamAAD.
func adapterParamAAD(adapterName, key string) string {
	return crypto.AAD("syncadapter", adapterName, key)
}

// EncryptParams wraps every plaintext string value in the named
// adapter's Params under masterKey, in place. Envelopes already present
// are left untouched. Mirrors the config save pipeline's encrypt-by-
// default behaviour so host/path/port for an adapter are never written
// in cleartext when they came from a prompt.
func EncryptParams(masterKey []byte, a *Adapter) error {
	if a.Params == nil {
		return nil
	}
	for k, v := range a.Params {
		s, ok := v.(string)
		if !ok || s == "" || crypto.IsEnvelope(s) {
			continue
		}
		env, err := crypto.EncryptField(masterKey, s, adapterParamAAD(a.Name, k))
		if err != nil {
			return err
		}
		a.Params[k] = env
	}
	return nil
}

// DecryptParams returns a copy of the adapter's Params with every
// envelope string replaced by its plaintext, leaving the on-disk
// structure untouched. The returned map is what gets handed to the
// adapter Constructor.
func DecryptParams(masterKey []byte, a *Adapter) (map[string]any, error) {
	out := make(map[string]any, len(a.Params))
	for k, v := range a.Params {
		s, ok := v.(string)
		if !ok {
			out[k] = v
			continue
		}
		if !crypto.IsEnvelope(s) {
			out[k] = s
			continue
		}
		pt, err := crypto.DecryptField(masterKey, s, adapterParamAAD(a.Name, k))
		if err != nil {
			return nil, err
		}
		out[k] = pt
	}
	return out, nil
}
