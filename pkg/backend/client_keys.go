package backend

import (
	"fmt"
)

// SecretStoreKey and ClusterStoreKey are the ONLY places the store client
// key formats are defined; the field indexer side (storeRefKey), the query
// side (resolveClientName, map functions), the Store controllers' provider
// registration/deletion (same key space) and the auth chain
// (BuildAuthConfig) must all go through them so the index keys, lookup keys
// and registry keys can never drift.
func SecretStoreKey(namespace, name string) string {
	return fmt.Sprintf("namespace/%s/%s", namespace, name)
}
func ClusterStoreKey(name string) string { return fmt.Sprintf("cluster/%s", name) }

// CompositeClientKey is the ONLY place the composite client cache key
// "clientName#endpoint" is constructed; the controller-side cache key and
// the provider-side registration key must both go through it so they can
// never drift. An empty endpoint returns clientName unchanged (default
// endpoint clients live under the plain clientName key).
func CompositeClientKey(clientName, endpoint string) string {
	if endpoint == "" {
		return clientName
	}
	return fmt.Sprintf("%s#%s", clientName, endpoint)
}
