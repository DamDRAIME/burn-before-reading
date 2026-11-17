package users

import "github.com/google/uuid"

func deconflictPrinterAccesses(printerAccesses []PrinterAccess) ([]PrinterAccess, error) {
	seen := make(map[uuid.UUID]Role, len(printerAccesses))
	dedupedPrinterAccesses := make([]PrinterAccess, 0, len(printerAccesses))
	for _, pa := range printerAccesses {
		if seenRole, ok := seen[pa.PrinterID]; ok {
			if seenRole != pa.Role {
				return nil, ErrConflictingRoles
			}
			continue // Duplicate with the same role, skip it.
		}
		seen[pa.PrinterID] = pa.Role
		dedupedPrinterAccesses = append(dedupedPrinterAccesses, pa)
	}
	return dedupedPrinterAccesses, nil
}