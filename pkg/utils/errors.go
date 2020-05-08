/*

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package utils

import (
	"errors"
	"fmt"
)

// Error constants
var (
	ErrInvalidKey     = errors.New("key is invalid")
	ErrInvalidKeyType = errors.New("key is of invalid type")
)

// The errors that might occur when parsing and validating a token
const (
	UnknownErrorType                     = "UnknownError"
	BackendNotImplementedErrorType       = "BackendNotImplementedError"
	BackendSecretNotFoundErrorType       = "BackendSecretNotFoundError"
	BackendSecretTypeNotSupportErrorType = "BackendSecretTypeNotSupportError"
	EmptySecretKeyErrorType              = "EmptySeretKeyError"
)

// BackendNotImplementedError will be raised if the selected backend is not implemented
type BackendNotImplementedError struct {
	ErrType string
	Backend string
}

// BackendSecretNotFoundError will be raised if secret is not found in the selected backend
type BackendSecretNotFoundError struct {
	ErrType string
	Key     string
}

// EmptySecretKeyError will be raised if the selected encoding is not implemented
type EmptySecretKeyError struct {
	ErrType string
}

// BackendSecretTypeNotSupportError will be raised if the secret data type not support
type BackendSecretTypeNotSupportError struct {
	ErrType string
	Key     string
}

func getErrorType(err error) string {
	switch err.(type) {
	case *BackendNotImplementedError:
		return BackendNotImplementedErrorType
	case *BackendSecretNotFoundError:
		return BackendSecretNotFoundErrorType
	case *EmptySecretKeyError:
		return EmptySecretKeyErrorType
	case *BackendSecretTypeNotSupportError:
		return BackendSecretTypeNotSupportErrorType
	default:
		return UnknownErrorType
	}
}

func (e BackendNotImplementedError) Error() string {
	return fmt.Sprintf("[%s] backend %s not supported", e.ErrType, e.Backend)
}

func (e BackendSecretNotFoundError) Error() string {
	return fmt.Sprintf("[%s] secret not found at %s", e.ErrType, e.Key)
}

func (e EmptySecretKeyError) Error() string {
	return fmt.Sprintf("[%s] empty key path given", e.ErrType)
}

func (e BackendSecretTypeNotSupportError) Error() string {
	return fmt.Sprintf("[%s] secret type not support at %s", e.ErrType, e.Key)
}

// IsBackendNotImplemented returns true if the error is type of BackendNotImplementedError and false otherwise
func IsBackendNotImplemented(err error) bool {
	return getErrorType(err) == BackendNotImplementedErrorType
}

// IsBackendSecretNotFound returns true if the error is type of BackendSecretNotFound and false otherwise
func IsBackendSecretNotFound(err error) bool {
	return getErrorType(err) == BackendSecretNotFoundErrorType
}

// IsEmptySecretKey returns true if the error is type of EmptySecretKeyError and false otherwise
func IsEmptySecretKey(err error) bool {
	return getErrorType(err) == EmptySecretKeyErrorType
}
