package input

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func Text(text string) error {
	if !utf8.ValidString(text) || strings.TrimSpace(text) == "" {
		return fmt.Errorf("text must be nonempty UTF-8")
	}
	if utf8.RuneCountInString(text) > 4000 {
		return fmt.Errorf("text exceeds 4000 runes")
	}
	return nil
}
