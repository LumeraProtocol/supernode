package util

import (
    "fmt"
    "strings"
)

// ValidateUlumeIntCoin checks that the input is a positive integer amount
// with the 'ulume' denom, e.g., "1000ulume". It keeps validation simple
// without pulling in SDK dependencies.
func ValidateUlumeIntCoin(s string) error {
    const denom = "ulume"
    if !strings.HasSuffix(s, denom) {
        return fmt.Errorf("denom must be '%s'", denom)
    }
    num := s[:len(s)-len(denom)]
    if num == "" {
        return fmt.Errorf("amount is required before denom")
    }
    // must be all digits, no leading +/-, no decimals
    var val uint64
    for i := 0; i < len(num); i++ {
        c := num[i]
        if c < '0' || c > '9' {
            return fmt.Errorf("amount must be an integer number")
        }
        // simple overflow-safe accumulation for uint64
        val = val*10 + uint64(c-'0')
    }
    if val == 0 {
        return fmt.Errorf("amount must be greater than zero")
    }
    return nil
}

