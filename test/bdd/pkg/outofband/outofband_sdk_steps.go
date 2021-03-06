/*
Copyright SecureKey Technologies Inc. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package outofband

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"
	"github.com/google/uuid"

	"github.com/hyperledger/aries-framework-go/pkg/client/outofband"
	"github.com/hyperledger/aries-framework-go/pkg/didcomm/protocol/decorator"
	"github.com/hyperledger/aries-framework-go/test/bdd/pkg/context"
	bddDIDExchange "github.com/hyperledger/aries-framework-go/test/bdd/pkg/didexchange"
)

// SDKSteps for the out-of-band protocol.
type SDKSteps struct {
	context         *context.BDDContext
	oobClients      map[string]*outofband.Client
	pendingRequests map[string]*outofband.Request
	connectionIDs   map[string]string
	bddDIDExchSDK   *bddDIDExchange.SDKSteps
}

// NewOutOfBandSDKSteps returns the out-of-band protocol's BDD steps using the SDK binding.
func NewOutOfBandSDKSteps() *SDKSteps {
	return &SDKSteps{
		oobClients:      make(map[string]*outofband.Client),
		pendingRequests: make(map[string]*outofband.Request),
		connectionIDs:   make(map[string]string),
		bddDIDExchSDK:   bddDIDExchange.NewDIDExchangeSDKSteps(),
	}
}

// SetContext is called before every scenario is run with a fresh new context
func (sdk *SDKSteps) SetContext(ctx *context.BDDContext) {
	sdk.context = ctx
	sdk.bddDIDExchSDK.SetContext(ctx)
}

// RegisterSteps registers the BDD steps on the suite.
func (sdk *SDKSteps) RegisterSteps(suite *godog.Suite) {
	suite.Step(
		`^"([^"]*)" constructs an out-of-band request with no attachments`, sdk.constructOOBRequestWithNoAttachments)
	suite.Step(
		`^"([^"]*)" sends the request to "([^"]*)" through an out-of-band channel`, sdk.sendRequestThruOOBChannel)
	suite.Step(`^"([^"]*)" accepts the request and connects with "([^"]*)"`, sdk.acceptRequestAndConnect)
	suite.Step(`^"([^"]*)" and "([^"]*)" confirm their connection is "([^"]*)"`, sdk.confirmConnections)
}

func (sdk *SDKSteps) constructOOBRequestWithNoAttachments(agentID string) error {
	err := sdk.registerClients(agentID)
	if err != nil {
		return fmt.Errorf("failed to register outofband client : %w", err)
	}

	req, err := sdk.newRequest(agentID)
	if err != nil {
		return fmt.Errorf("failed to create an out-of-bound request : %w", err)
	}

	sdk.pendingRequests[agentID] = req

	return nil
}

// sends a the sender's pending request to the receiver and returns the sender and receiver's new connection IDs.
func (sdk *SDKSteps) sendRequestThruOOBChannel(senderID, receiverID string) error {
	err := sdk.registerClients([]string{senderID, receiverID}...)
	if err != nil {
		return fmt.Errorf("failed to register framework clients : %w", err)
	}

	req, found := sdk.pendingRequests[senderID]
	if !found {
		return fmt.Errorf("no request found for %s", senderID)
	}

	delete(sdk.pendingRequests, senderID)

	sdk.pendingRequests[receiverID] = req

	return nil
}

func (sdk *SDKSteps) acceptRequestAndConnect(receiverID, senderID string) error {
	request, found := sdk.pendingRequests[receiverID]
	if !found {
		return fmt.Errorf("no pending requests found for %s", receiverID)
	}

	delete(sdk.pendingRequests, receiverID)

	receiver, found := sdk.oobClients[receiverID]
	if !found {
		return fmt.Errorf("no registered outofband client for %s", receiverID)
	}

	err := sdk.bddDIDExchSDK.RegisterPostMsgEvent(strings.Join([]string{senderID, receiverID}, ","), "completed")
	if err != nil {
		return fmt.Errorf("failed to register agents for didexchange post msg events : %w", err)
	}

	sdk.connectionIDs[receiverID], err = receiver.AcceptRequest(request)
	if err != nil {
		return fmt.Errorf("%s failed to accept out-of-band invitation : %w", receiverID, err)
	}

	err = sdk.bddDIDExchSDK.ApproveRequest(receiverID)
	if err != nil {
		return fmt.Errorf("failed to approve request for %s : %w", senderID, err)
	}

	err = sdk.bddDIDExchSDK.ApproveRequest(senderID)
	if err != nil {
		return fmt.Errorf("failed to approve request for %s : %w", senderID, err)
	}

	return nil
}

func (sdk *SDKSteps) confirmConnections(senderID, receiverID, status string) error {
	err := sdk.bddDIDExchSDK.WaitForPostEvent(strings.Join([]string{senderID, receiverID}, ","), status)
	if err != nil {
		return fmt.Errorf("failed to wait for post events : %w", err)
	}

	err = sdk.bddDIDExchSDK.ValidateConnection(senderID, status)
	if err != nil {
		return fmt.Errorf("failed to validate connection status for %s : %w", senderID, err)
	}

	err = sdk.bddDIDExchSDK.ValidateConnection(receiverID, status)
	if err != nil {
		return fmt.Errorf("failed to validate connection status for %s : %w", receiverID, err)
	}

	return nil
}

func (sdk *SDKSteps) registerClients(agentIDs ...string) error {
	for _, agent := range agentIDs {
		if _, exists := sdk.oobClients[agent]; !exists {
			client, err := outofband.New(sdk.context.AgentCtx[agent])
			if err != nil {
				return fmt.Errorf("failed to create new outofband client : %w", err)
			}

			sdk.oobClients[agent] = client
		}

		if _, exists := sdk.context.DIDExchangeClients[agent]; !exists {
			err := sdk.bddDIDExchSDK.CreateDIDExchangeClient(agent)
			if err != nil {
				return fmt.Errorf("failed to create new didexchange client : %w", err)
			}
		}
	}

	return nil
}

func (sdk *SDKSteps) newRequest(agentID string) (*outofband.Request, error) {
	agent, found := sdk.oobClients[agentID]
	if !found {
		return nil, fmt.Errorf("no agent for %s was found", agentID)
	}

	req, err := agent.CreateRequest(outofband.WithAttachments(&decorator.Attachment{
		ID:          uuid.New().String(),
		Description: "dummy",
		MimeType:    "text/plain",
		Data: decorator.AttachmentData{
			JSON: map[string]interface{}{},
		},
	}))
	if err != nil {
		return nil, fmt.Errorf("failed to create request for %s : %w", agentID, err)
	}

	return req, nil
}
