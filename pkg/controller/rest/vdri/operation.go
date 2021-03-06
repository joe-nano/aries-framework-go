/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package vdri

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/gorilla/mux"

	"github.com/hyperledger/aries-framework-go/pkg/controller/command/vdri"
	"github.com/hyperledger/aries-framework-go/pkg/controller/internal/cmdutil"
	"github.com/hyperledger/aries-framework-go/pkg/controller/rest"
	vdriapi "github.com/hyperledger/aries-framework-go/pkg/framework/aries/api/vdri"
	"github.com/hyperledger/aries-framework-go/pkg/storage"
)

const (
	vdriOperationID     = "/vdri"
	createPublicDIDPath = vdriOperationID + "/create-public-did"
	vdriDIDPath         = vdriOperationID + "/did"
	saveDIDPath         = vdriDIDPath
	getDIDPath          = vdriDIDPath + "/{id}"
	getDIDRecordsPath   = vdriDIDPath + "/records"
)

// provider contains dependencies for the common controller operations
// and is typically created by using aries.Context()
type provider interface {
	VDRIRegistry() vdriapi.Registry
	StorageProvider() storage.Provider
}

// Operation contains basic common operations provided by controller REST API
type Operation struct {
	handlers []rest.Handler
	command  *vdri.Command
}

// New returns new common operations rest client instance
func New(ctx provider) (*Operation, error) {
	cmd, err := vdri.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("new vdri : %w", err)
	}

	o := &Operation{command: cmd}
	o.registerHandler()

	return o, nil
}

// GetRESTHandlers get all controller API handler available for this service
func (o *Operation) GetRESTHandlers() []rest.Handler {
	return o.handlers
}

// registerHandler register handlers to be exposed from this protocol service as REST API endpoints
func (o *Operation) registerHandler() {
	// Add more protocol endpoints here to expose them as controller API endpoints
	o.handlers = []rest.Handler{
		cmdutil.NewHTTPHandler(createPublicDIDPath, http.MethodPost, o.CreatePublicDID),
		cmdutil.NewHTTPHandler(saveDIDPath, http.MethodPost, o.SaveDID),
		cmdutil.NewHTTPHandler(getDIDPath, http.MethodGet, o.GetDID),
		cmdutil.NewHTTPHandler(getDIDRecordsPath, http.MethodGet, o.GetDIDRecords),
	}
}

// CreatePublicDID swagger:route POST /vdri/create-public-did vdri createPublicDID
//
// Creates a new Public DID.
//
// Responses:
//    default: genericError
//        200: createPublicDIDResponse
func (o *Operation) CreatePublicDID(rw http.ResponseWriter, req *http.Request) {
	reqBytes, err := queryValuesAsJSON(req.URL.Query())
	if err != nil {
		rest.SendHTTPStatusError(rw, http.StatusBadRequest, vdri.InvalidRequestErrorCode, err)
		return
	}

	rest.Execute(o.command.CreatePublicDID, rw, bytes.NewReader(reqBytes))
}

// SaveDID swagger:route POST /vdri/did vdri saveDIDReq
//
// Saves a did document with the friendly name.
//
// Responses:
//    default: genericError
func (o *Operation) SaveDID(rw http.ResponseWriter, req *http.Request) {
	rest.Execute(o.command.SaveDID, rw, req.Body)
}

// GetDID swagger:route GET /vdri/did/{id} vdri getDIDReq
//
// Gets did document with the friendly name.
//
// Responses:
//    default: genericError
//        200: documentRes
func (o *Operation) GetDID(rw http.ResponseWriter, req *http.Request) {
	id := mux.Vars(req)["id"]

	decodedID, err := base64.StdEncoding.DecodeString(id)
	if err != nil {
		rest.SendHTTPStatusError(rw, http.StatusBadRequest, vdri.InvalidRequestErrorCode, fmt.Errorf("invalid id"))
		return
	}

	request := fmt.Sprintf(`{"id":"%s"}`, string(decodedID))

	rest.Execute(o.command.GetDID, rw, bytes.NewBufferString(request))
}

// GetDIDRecords swagger:route GET /vdri/did/records vdri getDIDRecords
//
// Retrieves the did records
//
// Responses:
//    default: genericError
//        200: didRecordResult
func (o *Operation) GetDIDRecords(rw http.ResponseWriter, req *http.Request) {
	rest.Execute(o.command.GetDIDRecords, rw, req.Body)
}

// queryValuesAsJSON converts query strings to `map[string]string`
// and marshals them to JSON bytes
func queryValuesAsJSON(vals url.Values) ([]byte, error) {
	// normalize all query string key/values
	args := make(map[string]string)

	for k, v := range vals {
		if len(v) > 0 {
			args[k] = v[0]
		}
	}

	return json.Marshal(args)
}
