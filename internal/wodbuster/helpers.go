package wodbuster

// cleanClassType removes extra whitespace and special characters from class type
func cleanClassType(classType string) string {
	// Remove leading/trailing whitespace
	classType = trimSpace(classType)
	// Remove asterisks that indicate additional info
	if len(classType) > 0 && classType[len(classType)-1] == '*' {
		classType = classType[:len(classType)-1]
	}
	return trimSpace(classType)
}

// trimSpace removes leading and trailing whitespace
func trimSpace(s string) string {
	start := 0
	end := len(s)

	// Find first non-space character
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}

	// Find last non-space character
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}

	return s[start:end]
}
