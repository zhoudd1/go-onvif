package onvif

import (
	"errors"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/clbanning/mxj"
	"github.com/satori/go.uuid"
)

var errWrongDiscoveryResponse = errors.New("Response is not related to discovery request")

// StartDiscovery send a WS-Discovery message and wait for all matching device to respond
func StartDiscovery(duration time.Duration) ([]Device, error) {
	// Create initial discovery results
	discoveryResults := []Device{}

	// Create WS-Discovery request
	requestID := "uuid:" + uuid.NewV4().String()
	request := `<?xml version="1.0" encoding="UTF-8"?>
		<s:Envelope
			xmlns:s="http://www.w3.org/2003/05/soap-envelope"
			xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing">
			<s:Header>
				<a:Action s:mustUnderstand="1">http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</a:Action>
				<a:MessageID>` + requestID + `</a:MessageID>
				<a:ReplyTo>
					<a:Address>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</a:Address>
				</a:ReplyTo>
				<a:To s:mustUnderstand="1">urn:schemas-xmlsoap-org:ws:2005:04:discovery</a:To>
			</s:Header>
			<s:Body>
				<Probe
					xmlns="http://schemas.xmlsoap.org/ws/2005/04/discovery">
					<d:Types
						xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"
						xmlns:dp0="http://www.onvif.org/ver10/network/wsdl">dp0:NetworkVideoTransmitter
					</d:Types>
				</Probe>
			</s:Body>
		</s:Envelope>`

	// Clean WS-Discovery message
	request = regexp.MustCompile(`\>\s+\<`).ReplaceAllString(request, "><")
	request = regexp.MustCompile(`\s+`).ReplaceAllString(request, " ")

	// Create UDP address for local and multicast address
	localAddress, err := net.ResolveUDPAddr("udp4", ":0")
	if err != nil {
		return discoveryResults, err
	}

	multicastAddress, err := net.ResolveUDPAddr("udp4", "239.255.255.250:3702")
	if err != nil {
		return discoveryResults, err
	}

	// Create UDP connection to listen for respond from matching device
	conn, err := net.ListenUDP("udp", localAddress)
	if err != nil {
		return discoveryResults, err
	}
	defer conn.Close()

	// Set connection's timeout
	err = conn.SetDeadline(time.Now().Add(duration))
	if err != nil {
		return discoveryResults, err
	}

	// Send WS-Discovery request to multicast address
	_, err = conn.WriteToUDP([]byte(request), multicastAddress)
	if err != nil {
		return discoveryResults, err
	}

	// Keep reading UDP message until timeout
	for {
		// Create buffer and receive UDP response
		buffer := make([]byte, 10*1024)
		_, _, err = conn.ReadFromUDP(buffer)

		// Check if connection timeout
		if err != nil {
			if udpErr, ok := err.(net.Error); ok && udpErr.Timeout() {
				break
			} else {
				return discoveryResults, err
			}
		}

		// Read and parse WS-Discovery response
		device, err := readDiscoveryResponse(requestID, buffer)
		if err != nil && err != errWrongDiscoveryResponse {
			return discoveryResults, err
		}

		// Push device to results
		discoveryResults = append(discoveryResults, device)
	}

	return discoveryResults, nil
}

// readDiscoveryResponse reads and parses WS-Discovery response
func readDiscoveryResponse(messageID string, buffer []byte) (Device, error) {
	// Inital result
	result := Device{}

	// Parse XML to map
	mapXML, err := mxj.NewMapXml(buffer)
	if err != nil {
		return result, err
	}

	// Check if this response is for our request
	responseMessageID, _ := mapXML.ValueForPathString("Envelope.Header.RelatesTo")
	if responseMessageID != messageID {
		return result, errWrongDiscoveryResponse
	}

	// Get device's ID and clean it
	deviceID, _ := mapXML.ValueForPathString("Envelope.Body.ProbeMatches.ProbeMatch.EndpointReference.Address")
	deviceID = strings.Replace(deviceID, "urn:uuid", "", 1)

	// Get device's name
	deviceName := ""
	scopes, _ := mapXML.ValueForPathString("Envelope.Body.ProbeMatches.ProbeMatch.Scopes")
	for _, scope := range strings.Split(scopes, " ") {
		if strings.HasPrefix(scope, "onvif://www.onvif.org/name/") {
			deviceName = strings.Replace(scope, "onvif://www.onvif.org/name/", "", 1)
			deviceName = strings.Replace(deviceName, "_", " ", -1)
			break
		}
	}

	// Get device's xAddrs
	xAddrs, _ := mapXML.ValueForPathString("Envelope.Body.ProbeMatches.ProbeMatch.XAddrs")
	listXAddr := strings.Split(xAddrs, " ")
	if len(listXAddr) == 0 {
		return result, errors.New("Device does not have any xAddr")
	}

	// Finalize result
	result.ID = deviceID
	result.Name = deviceName
	result.XAddr = listXAddr[0]

	return result, nil
}