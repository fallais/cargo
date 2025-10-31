package root

import (
	"fmt"

	"cargo/internal/obd/serial"
	"cargo/pkg/log"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

func Run(cmd *cobra.Command, args []string) {
	// Connect to ELM327
	elm, err := serial.NewELM327("COM6")
	if err != nil {
		log.Fatal("Failed to connect", zap.Error(err))
	}
	defer elm.Close()

	// Example queries
	fmt.Println("\n=== Querying OBD Data ===")

	// Get ALL DTCs from all modules
	allDTCs, err := elm.GetAllDTCs()
	if err != nil {
		log.Error("Error getting DTCs", zap.Error(err))
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
		log.Error("Query error", zap.Error(err))
	} else {
		log.Info("Coolant temp response", zap.String("response", resp))
	}

	// Uncomment to clear DTCs (use with caution!)
	// err = elm.ClearDTCs()
	// if err != nil {
	// 	log.Printf("Error clearing DTCs: %v", err)
	// }
}
