package config

import (
	"bufio"
	"os"
	"strings"
)

// LoadEnv doc file .env va nap tat ca cac bien vao os.Setenv
func LoadEnv(filePath string) error {
	if filePath == "" {
		filePath = ".env"
	}

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Bo qua dong trong hoac chu thich bat dau bang #
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Tach bien theo dau =
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])

			// Cat bo dau ngoac kep neu co
			value = strings.Trim(value, "\"")
			value = strings.Trim(value, "'")

			if key != "" {
				_ = os.Setenv(key, value)
			}
		}
	}

	return scanner.Err()
}
