package serial

import (
	"bufio"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/tarm/serial"
)

// ELM327 represents an ELM327 OBD-II adapter connection
type ELM327 struct {
	port     *serial.Port
	reader   *bufio.Reader
	portName string
	baudRate int
	protocol string
}

// NewELM327 creates a new ELM327 connection
func NewELM327(portName string) (*ELM327, error) {
	elm := &ELM327{
		portName: portName,
		baudRate: 38400, // Default, will auto-detect
	}

	if err := elm.connect(); err != nil {
		return nil, err
	}

	return elm, nil
}

// connect establishes connection and initializes the ELM327
func (e *ELM327) connect() error {
	// Try common baud rates
	baudRates := []int{38400, 9600, 115200, 230400}

	for _, baud := range baudRates {
		log.Printf("Attempting baud rate: %d", baud)

		config := &serial.Config{
			Name:        e.portName,
			Baud:        baud,
			ReadTimeout: time.Millisecond * 100, // Shorter timeout for read attempts
		}

		port, err := serial.OpenPort(config)
		if err != nil {
			log.Printf("Failed to open port at baud %d: %v", baud, err)
			continue
		}

		e.port = port
		e.baudRate = baud
		e.reader = bufio.NewReader(port)

		// Give the device a moment to settle
		time.Sleep(100 * time.Millisecond)

		// Try to communicate
		if err := e.initialize(); err != nil {
			log.Printf("Initialization failed at baud %d: %v", baud, err)
			port.Close()
			continue
		}

		log.Printf("Connected successfully: PORT=%s BAUD=%d PROTOCOL=%s",
			e.portName, e.baudRate, e.protocol)
		return nil
	}

	return fmt.Errorf("failed to connect on any baud rate")
}

// initialize sends initialization commands to ELM327
func (e *ELM327) initialize() error {
	// Flush any pending data
	e.port.Flush()

	// Reset device
	log.Println("write: 'ATZ'")
	if err := e.writeCommand("ATZ"); err != nil {
		return err
	}

	// Wait longer for reset
	time.Sleep(1500 * time.Millisecond)

	// Try to read response
	resp, err := e.readResponse()
	if err != nil {
		log.Printf("Error reading ATZ response: %v", err)
		return err
	}
	log.Printf("read: %q", resp)

	// Check for ELM327 identifier
	if !strings.Contains(resp, "ELM327") && !strings.Contains(resp, "ELM") {
		return fmt.Errorf("no ELM327 response detected in: %q", resp)
	}

	// Small delay between commands
	time.Sleep(100 * time.Millisecond)

	// Echo off
	log.Println("write: 'ATE0'")
	if err := e.writeCommand("ATE0"); err != nil {
		return err
	}
	resp, err = e.readResponse()
	if err != nil {
		return err
	}
	log.Printf("read: %q", resp)

	time.Sleep(100 * time.Millisecond)

	// Headers on
	log.Println("write: 'ATH1'")
	if err := e.writeCommand("ATH1"); err != nil {
		return err
	}
	resp, _ = e.readResponse()
	log.Printf("read: %q", resp)

	time.Sleep(100 * time.Millisecond)

	// Linefeeds off
	log.Println("write: 'ATL0'")
	if err := e.writeCommand("ATL0"); err != nil {
		return err
	}
	resp, _ = e.readResponse()
	log.Printf("read: %q", resp)

	time.Sleep(100 * time.Millisecond)

	// Get voltage
	log.Println("write: 'AT RV'")
	if err := e.writeCommand("AT RV"); err != nil {
		return err
	}
	resp, _ = e.readResponse()
	log.Printf("read: %q (voltage)", resp)

	time.Sleep(100 * time.Millisecond)

	// Auto-detect protocol
	log.Println("Auto-detecting protocol...")
	if err := e.autoDetectProtocol(); err != nil {
		return fmt.Errorf("protocol detection failed: %v", err)
	}

	return nil
}

// autoDetectProtocol attempts to automatically detect the vehicle's protocol
func (e *ELM327) autoDetectProtocol() error {
	// Set protocol to auto (0)
	log.Println("write: 'ATSP0'")
	if err := e.writeCommand("ATSP0"); err != nil {
		return err
	}
	resp, _ := e.readResponse()
	log.Printf("read: %s", resp)

	// Try to communicate - this will make ELM327 auto-detect
	log.Println("write: '0100' (triggering auto-detection)")
	if err := e.writeCommand("0100"); err != nil {
		return err
	}
	resp, err := e.readResponse()
	if err != nil {
		return err
	}
	log.Printf("read: %s", resp)

	// Check if we got a valid response
	if strings.Contains(resp, "NO DATA") || strings.Contains(resp, "ERROR") ||
		strings.Contains(resp, "UNABLE TO CONNECT") {
		return fmt.Errorf("unable to detect protocol: %s", resp)
	}

	// Get the protocol that was detected
	log.Println("write: 'ATDPN'")
	if err := e.writeCommand("ATDPN"); err != nil {
		return err
	}
	resp, _ = e.readResponse()
	log.Printf("read: %s", resp)

	// Parse protocol number
	protocolNum := strings.TrimSpace(strings.ReplaceAll(resp, ">", ""))
	e.protocol = protocolNum

	protocolName := e.getProtocolName(protocolNum)
	log.Printf("Protocol detected: %s (%s)", protocolNum, protocolName)

	return nil
}

// getProtocolName returns human-readable protocol name
func (e *ELM327) getProtocolName(protocolNum string) string {
	protocols := map[string]string{
		"0": "Auto",
		"1": "SAE J1850 PWM (41.6 kbaud)",
		"2": "SAE J1850 VPW (10.4 kbaud)",
		"3": "ISO 9141-2 (5 baud init)",
		"4": "ISO 14230-4 KWP (5 baud init)",
		"5": "ISO 14230-4 KWP (fast init)",
		"6": "ISO 15765-4 CAN (11 bit ID, 500 kbaud)",
		"7": "ISO 15765-4 CAN (29 bit ID, 500 kbaud)",
		"8": "ISO 15765-4 CAN (11 bit ID, 250 kbaud)",
		"9": "ISO 15765-4 CAN (29 bit ID, 250 kbaud)",
		"A": "SAE J1939 CAN (29 bit ID, 250 kbaud)",
	}

	if name, ok := protocols[protocolNum]; ok {
		return name
	}
	return "Unknown"
}

// writeCommand sends a command to the ELM327
func (e *ELM327) writeCommand(cmd string) error {
	_, err := e.port.Write([]byte(cmd + "\r"))
	return err
}

// readResponse reads response from ELM327 until prompt '>'
func (e *ELM327) readResponse() (string, error) {
	var response strings.Builder
	buffer := make([]byte, 1)
	timeout := time.Now().Add(5 * time.Second)

	for {
		// Check timeout
		if time.Now().After(timeout) {
			return "", fmt.Errorf("timeout reading response")
		}

		// Read one byte at a time
		n, err := e.port.Read(buffer)
		if err != nil {
			// Timeout on read is not fatal, continue
			if err.Error() == "EOF" {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return response.String(), err
		}

		if n > 0 {
			response.WriteByte(buffer[0])

			// Check if we got the prompt
			if buffer[0] == '>' {
				break
			}

			// Reset timeout on successful read
			timeout = time.Now().Add(5 * time.Second)
		}
	}

	return strings.TrimSpace(response.String()), nil
}

// Query sends an OBD command and returns the response
func (e *ELM327) Query(cmd string) (string, error) {
	if err := e.writeCommand(cmd); err != nil {
		return "", err
	}
	return e.readResponse()
}

// GetRPM queries engine RPM (PID 0x0C)
func (e *ELM327) GetRPM() (int, error) {
	resp, err := e.Query("010C")
	if err != nil {
		return 0, err
	}

	// Parse response (implementation depends on your needs)
	// Format: "41 0C XX XX" where XXXX is RPM/4
	log.Printf("RPM response: %s", resp)
	return 0, nil // Parse the actual value
}

// GetSpeed queries vehicle speed (PID 0x0D)
func (e *ELM327) GetSpeed() (int, error) {
	resp, err := e.Query("010D")
	if err != nil {
		return 0, err
	}

	log.Printf("Speed response: %s", resp)
	return 0, nil // Parse the actual value
}

// Close closes the connection
func (e *ELM327) Close() error {
	if e.port != nil {
		return e.port.Close()
	}
	return nil
}

// DTC represents a Diagnostic Trouble Code
type DTC struct {
	Code        string
	Description string
}

// GetDTCs retrieves all stored diagnostic trouble codes
func (e *ELM327) GetDTCs() ([]DTC, error) {
	log.Println("\n=== Retrieving DTCs ===")

	// Request stored DTCs (Mode 03)
	log.Println("write: '03'")
	resp, err := e.Query("03")
	if err != nil {
		return nil, fmt.Errorf("failed to query DTCs: %v", err)
	}

	log.Printf("DTC response: %s", resp)

	// Parse DTCs from response
	dtcs := e.parseDTCs(resp)

	return dtcs, nil
}

// GetPendingDTCs retrieves pending diagnostic trouble codes
func (e *ELM327) GetPendingDTCs() ([]DTC, error) {
	log.Println("\n=== Retrieving Pending DTCs ===")

	// Request pending DTCs (Mode 07)
	log.Println("write: '07'")
	resp, err := e.Query("07")
	if err != nil {
		return nil, fmt.Errorf("failed to query pending DTCs: %v", err)
	}

	log.Printf("Pending DTC response: %s", resp)

	dtcs := e.parseDTCs(resp)

	return dtcs, nil
}

// ClearDTCs clears all diagnostic trouble codes and MIL (Check Engine Light)
func (e *ELM327) ClearDTCs() error {
	log.Println("\n=== Clearing DTCs ===")

	// Clear DTCs (Mode 04)
	log.Println("write: '04'")
	resp, err := e.Query("04")
	if err != nil {
		return fmt.Errorf("failed to clear DTCs: %v", err)
	}

	log.Printf("Clear DTC response: %s", resp)

	if strings.Contains(resp, "44") || strings.Contains(resp, "OK") {
		log.Println("DTCs cleared successfully")
		return nil
	}

	return fmt.Errorf("failed to clear DTCs: unexpected response")
}

// GetTPMSDTCs retrieves TPMS (Tire Pressure Monitoring System) DTCs
// TPMS codes are often stored in a separate module (C-codes)
func (e *ELM327) GetTPMSDTCs() ([]DTC, error) {
	log.Println("\n=== Retrieving TPMS/Chassis DTCs ===")

	var allDTCs []DTC

	// Try different ECU addresses for TPMS
	// Common TPMS module addresses
	tpmsAddresses := []string{
		"7C0", // TPMS module (some manufacturers)
		"7C4", // TPMS alternative
		"765", // Body Control Module
		"760", // Chassis
	}

	// Set header to query specific modules
	for _, addr := range tpmsAddresses {
		log.Printf("Querying module at address %s", addr)

		// Set header for specific module
		if err := e.writeCommand("ATSH" + addr); err != nil {
			continue
		}
		e.readResponse()

		// Try to get DTCs from this module
		resp, err := e.Query("03")
		if err != nil || strings.Contains(resp, "NO DATA") ||
			strings.Contains(resp, "ERROR") || strings.Contains(resp, "?") {
			continue
		}

		log.Printf("Response from %s: %s", addr, resp)
		dtcs := e.parseDTCs(resp)
		allDTCs = append(allDTCs, dtcs...)
	}

	// Reset to default header
	e.writeCommand("ATSH7DF")
	e.readResponse()

	return allDTCs, nil
}

// GetAllDTCs retrieves DTCs from all modules (engine, chassis, body, TPMS, etc.)
func (e *ELM327) GetAllDTCs() (map[string][]DTC, error) {
	log.Println("\n=== Retrieving DTCs from All Modules ===")

	allModuleDTCs := make(map[string][]DTC)

	// Standard engine DTCs
	engineDTCs, _ := e.GetDTCs()
	if len(engineDTCs) > 0 {
		allModuleDTCs["Engine/Powertrain"] = engineDTCs
	}

	// Pending DTCs
	pendingDTCs, _ := e.GetPendingDTCs()
	if len(pendingDTCs) > 0 {
		allModuleDTCs["Pending"] = pendingDTCs
	}

	// TPMS/Chassis DTCs
	tpmsDTCs, _ := e.GetTPMSDTCs()
	if len(tpmsDTCs) > 0 {
		allModuleDTCs["TPMS/Chassis"] = tpmsDTCs
	}

	// Try to query additional modules with mode 03
	additionalModules := []struct {
		name   string
		header string
	}{
		{"ABS", "7E1"},
		{"Airbag", "7E2"},
		{"Transmission", "7E3"},
		{"Body Control", "765"},
	}

	for _, mod := range additionalModules {
		// Set header
		e.writeCommand("ATSH" + mod.header)
		e.readResponse()

		resp, err := e.Query("03")
		if err == nil && !strings.Contains(resp, "NO DATA") &&
			!strings.Contains(resp, "ERROR") && !strings.Contains(resp, "?") {
			dtcs := e.parseDTCs(resp)
			if len(dtcs) > 0 {
				allModuleDTCs[mod.name] = dtcs
			}
		}
	}

	// Reset to default header
	e.writeCommand("ATSH7DF")
	e.readResponse()

	return allModuleDTCs, nil
}

// parseDTCs parses DTC codes from OBD response
func (e *ELM327) parseDTCs(response string) []DTC {
	var dtcs []DTC

	// Remove prompt and clean response
	response = strings.ReplaceAll(response, ">", "")
	response = strings.ReplaceAll(response, "\r", "")
	response = strings.ReplaceAll(response, "\n", "")

	// Split by spaces to handle CAN format (e.g., "7E8 02 43 00")
	parts := strings.Fields(response)

	// Find the mode response byte (43 for mode 03, 47 for mode 07)
	modeIdx := -1
	for i, part := range parts {
		if part == "43" || part == "47" {
			modeIdx = i
			break
		}
	}

	if modeIdx == -1 || modeIdx+1 >= len(parts) {
		return dtcs
	}

	// Next byte is the number of DTCs
	numDTCs := 0
	if modeIdx+1 < len(parts) {
		fmt.Sscanf(parts[modeIdx+1], "%x", &numDTCs)
	}

	// Check for no codes
	if numDTCs == 0 {
		return dtcs
	}

	// Parse DTC codes (each DTC is 2 bytes = 2 hex values)
	dtcStart := modeIdx + 2
	for i := dtcStart; i < len(parts) && i+1 < len(parts); i += 2 {
		byte1 := parts[i]
		byte2 := parts[i+1]

		// Skip padding bytes (AA, FF, 00)
		if byte1 == "AA" || byte1 == "FF" || (byte1 == "00" && byte2 == "00") {
			break
		}

		dtcHex := byte1 + byte2

		// Parse the DTC code
		code := e.decodeDTC(dtcHex)
		if code != "" {
			dtcs = append(dtcs, DTC{
				Code:        code,
				Description: e.getDTCDescription(code),
			})

			// Stop after we've read the expected number of DTCs
			if len(dtcs) >= numDTCs {
				break
			}
		}
	}

	return dtcs
}

// decodeDTC converts hex DTC to standard format (e.g., P0301)
func (e *ELM327) decodeDTC(hexCode string) string {
	if len(hexCode) != 4 {
		return ""
	}

	// Parse first byte
	firstByte := hexCode[0:2]
	var firstChar byte
	var firstDigit byte

	// Determine DTC type from first two bits
	switch firstByte[0] {
	case '0', '1', '2', '3':
		firstChar = 'P' // Powertrain
	case '4', '5', '6', '7':
		firstChar = 'C' // Chassis
	case '8', '9', 'A', 'B':
		firstChar = 'B' // Body
	case 'C', 'D', 'E', 'F':
		firstChar = 'U' // Network
	default:
		return ""
	}

	// Get the first digit (0-3)
	fb, _ := parseHexByte(firstByte)
	firstDigit = '0' + byte(fb&0x3)

	// Get remaining 3 digits
	remaining := hexCode[1:4]

	return fmt.Sprintf("%c%c%s", firstChar, firstDigit, strings.ToUpper(remaining))
}

// parseHexByte converts a 2-character hex string to byte
func parseHexByte(hex string) (byte, error) {
	var result byte
	_, err := fmt.Sscanf(hex, "%x", &result)
	return result, err
}

// getDTCDescription returns a description for common DTCs
func (e *ELM327) getDTCDescription(code string) string {
	descriptions := map[string]string{
		// Powertrain codes
		"P0300": "Random/Multiple Cylinder Misfire Detected",
		"P0301": "Cylinder 1 Misfire Detected",
		"P0302": "Cylinder 2 Misfire Detected",
		"P0303": "Cylinder 3 Misfire Detected",
		"P0304": "Cylinder 4 Misfire Detected",
		"P0420": "Catalyst System Efficiency Below Threshold",
		"P0171": "System Too Lean (Bank 1)",
		"P0172": "System Too Rich (Bank 1)",
		"P0174": "System Too Lean (Bank 2)",
		"P0175": "System Too Rich (Bank 2)",
		"P0101": "Mass Air Flow Circuit Range/Performance",
		"P0102": "Mass Air Flow Circuit Low Input",
		"P0103": "Mass Air Flow Circuit High Input",
		"P0401": "Exhaust Gas Recirculation Flow Insufficient",
		"P0402": "Exhaust Gas Recirculation Flow Excessive",
		"P0440": "Evaporative Emission Control System Malfunction",
		"P0441": "Evaporative Emission Control System Incorrect Purge Flow",
		"P0442": "Evaporative Emission Control System Leak Detected (Small)",
		"P0443": "Evaporative Emission Control System Purge Control Valve Circuit",
		"P0455": "Evaporative Emission Control System Leak Detected (Large)",
		"P0500": "Vehicle Speed Sensor Malfunction",
		"P0505": "Idle Control System Malfunction",
		"P0506": "Idle Control System RPM Lower Than Expected",
		"P0507": "Idle Control System RPM Higher Than Expected",

		// Chassis codes (TPMS and suspension)
		"C1A00": "TPMS Control Module Malfunction",
		"C1A01": "TPMS Module Configuration Error",
		"C1A02": "TPMS RF Receiver Malfunction",
		"C1A11": "Tire Pressure Sensor LF Malfunction",
		"C1A12": "Tire Pressure Sensor RF Malfunction",
		"C1A13": "Tire Pressure Sensor RR Malfunction",
		"C1A14": "Tire Pressure Sensor LR Malfunction",
		"C1A15": "TPMS System Malfunction",
		"C2100": "Tire Pressure Too Low - Left Front",
		"C2101": "Tire Pressure Too Low - Right Front",
		"C2102": "Tire Pressure Too Low - Right Rear",
		"C2103": "Tire Pressure Too Low - Left Rear",
		"C2111": "Tire Pressure Sensor Battery Low - LF",
		"C2112": "Tire Pressure Sensor Battery Low - RF",
		"C2113": "Tire Pressure Sensor Battery Low - RR",
		"C2114": "Tire Pressure Sensor Battery Low - LR",
		"C2200": "Tire Pressure Sensor Missing - LF",
		"C2201": "Tire Pressure Sensor Missing - RF",
		"C2202": "Tire Pressure Sensor Missing - RR",
		"C2203": "Tire Pressure Sensor Missing - LR",

		// Body codes
		"B1000": "Body Control Module Malfunction",
		"B1342": "ECU Defective",
		"B1600": "Ignition Switch Malfunction",

		// Network codes
		"U0001": "High Speed CAN Communication Bus",
		"U0100": "Lost Communication With ECM/PCM",
		"U0101": "Lost Communication With TCM",
		"U0121": "Lost Communication With ABS Module",
		"U0140": "Lost Communication With Body Control Module",
		"U0155": "Lost Communication With Instrument Cluster",
	}

	if desc, ok := descriptions[code]; ok {
		return desc
	}

	// Check for generic TPMS codes
	if strings.HasPrefix(code, "C1A") || strings.HasPrefix(code, "C2") {
		return "TPMS/Tire Pressure Related Code"
	}

	return "Unknown DTC"
}

func main() {
	// Connect to ELM327
	elm, err := NewELM327("/dev/rfcomm0")
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer elm.Close()

	fmt.Println("\n=== Querying OBD Data ===")

	// Get ALL DTCs from all modules
	allDTCs, err := elm.GetAllDTCs()
	if err != nil {
		log.Printf("Error getting DTCs: %v", err)
	}

	if len(allDTCs) == 0 {
		fmt.Println("No DTCs found in any module")
	} else {
		fmt.Printf("\nFound DTCs in %d module(s):\n", len(allDTCs))
		for module, dtcs := range allDTCs {
			fmt.Printf("\n--- %s Module ---\n", module)
			for _, dtc := range dtcs {
				fmt.Printf("  %s - %s\n", dtc.Code, dtc.Description)
			}
		}
	}

	// You can also query specific modules:
	fmt.Println("\n=== Standard Engine DTCs ===")
	dtcs, _ := elm.GetDTCs()
	if len(dtcs) == 0 {
		fmt.Println("No stored DTCs found")
	} else {
		for _, dtc := range dtcs {
			fmt.Printf("  %s - %s\n", dtc.Code, dtc.Description)
		}
	}

	// TPMS specific
	fmt.Println("\n=== TPMS/Chassis DTCs ===")
	tpmsDTCs, _ := elm.GetTPMSDTCs()
	if len(tpmsDTCs) == 0 {
		fmt.Println("No TPMS/Chassis DTCs found")
	} else {
		for _, dtc := range tpmsDTCs {
			fmt.Printf("  %s - %s\n", dtc.Code, dtc.Description)
		}
	}

	// Get RPM
	elm.GetRPM()

	// Get Speed
	elm.GetSpeed()

	// Custom query
	resp, err := elm.Query("0105") // Engine coolant temperature
	if err != nil {
		log.Printf("Query error: %v", err)
	} else {
		log.Printf("Coolant temp response: %s", resp)
	}

	// Uncomment to clear DTCs (use with caution!)
	// err = elm.ClearDTCs()
	// if err != nil {
	// 	log.Printf("Error clearing DTCs: %v", err)
	// }
}
