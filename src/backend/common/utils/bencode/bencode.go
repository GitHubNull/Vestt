// Package bencode implements the minimal bencode parse/re-encode needed by
// the download proxy: upstream DownloadController re-encodes every torrent
// through BencodeParser().Parse(...).EncodeAsBytes() so that dictionaries
// with unsorted keys become Sonarr-compatible.
//
// Observable upstream behaviour reproduced here:
//   - integers parse into int64 and re-encode canonically (no leading zeros);
//   - dictionary keys re-encode sorted bytewise (bencode spec order);
//   - byte strings round-trip verbatim.
//
// Ported from: Jackett.Server/Controllers/DownloadController.cs (usage) and
// BencodeNET (NuGet) observable semantics.
package bencode

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
)

// Value is one bencode node: int64, []byte (byte string), []Value (list)
// or map[string]Value (dictionary).
type Value = any

// Parse parses one bencode document, rejecting trailing garbage.
func Parse(data []byte) (Value, error) {	v, rest, err := parseValue(data)
	if err != nil {
		return nil, err
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("bencode: %d trailing bytes", len(rest))
	}
	return v, nil
}

// parseValue parses one node from the front of data, returning the rest.
func parseValue(data []byte) (Value, []byte, error) {
	if len(data) == 0 {
		return nil, nil, errors.New("bencode: unexpected end of input")
	}
	switch data[0] {
	case 'i':
		end := indexByte(data, 'e', 1)
		if end < 0 {
			return nil, nil, errors.New("bencode: unterminated integer")
		}
		n, err := strconv.ParseInt(string(data[1:end]), 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("bencode: bad integer: %w", err)
		}
		return n, data[end+1:], nil
	case 'l':
		var list []Value
		rest := data[1:]
		for {
			if len(rest) == 0 {
				return nil, nil, errors.New("bencode: unterminated list")
			}
			if rest[0] == 'e' {
				return list, rest[1:], nil
			}
			v, r, err := parseValue(rest)
			if err != nil {
				return nil, nil, err
			}
			list = append(list, v)
			rest = r
		}
	case 'd':
		dict := map[string]Value{}
		rest := data[1:]
		for {
			if len(rest) == 0 {
				return nil, nil, errors.New("bencode: unterminated dictionary")
			}
			if rest[0] == 'e' {
				return dict, rest[1:], nil
			}
			key, r, err := parseString(rest)
			if err != nil {
				return nil, nil, err
			}
			v, r2, err := parseValue(r)
			if err != nil {
				return nil, nil, err
			}
			dict[string(key)] = v
			rest = r2
		}
	default:
		s, rest, err := parseString(data)
		if err != nil {
			return nil, nil, err
		}
		return s, rest, nil
	}
}

func parseString(data []byte) ([]byte, []byte, error) {
	colon := indexByte(data, ':', 0)
	if colon <= 0 || colon > 20 {
		return nil, nil, errors.New("bencode: bad string length")
	}
	length, err := strconv.Atoi(string(data[:colon]))
	if err != nil || length < 0 {
		return nil, nil, errors.New("bencode: bad string length")
	}
	if len(data) < colon+1+length {
		return nil, nil, errors.New("bencode: short string")
	}
	return data[colon+1 : colon+1+length], data[colon+1+length:], nil
}

func indexByte(b []byte, c byte, from int) int {
	for i := from; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// Encode renders one parsed Value in canonical form: dictionary keys sorted
// bytewise, integers without leading zeros.
func Encode(v Value) ([]byte, error) {
	var buf []byte
	if err := encodeInto(&buf, v); err != nil {
		return nil, err
	}
	return buf, nil
}

func encodeInto(buf *[]byte, v Value) error {
	switch t := v.(type) {
	case int64:
		*buf = append(*buf, 'i')
		*buf = strconv.AppendInt(*buf, t, 10)
		*buf = append(*buf, 'e')
		return nil
	case []byte:
		*buf = strconv.AppendInt(*buf, int64(len(t)), 10)
		*buf = append(*buf, ':')
		*buf = append(*buf, t...)
		return nil
	case string:
		return encodeInto(buf, []byte(t))
	case []Value:
		*buf = append(*buf, 'l')
		for _, item := range t {
			if err := encodeInto(buf, item); err != nil {
				return err
			}
		}
		*buf = append(*buf, 'e')
		return nil
	case map[string]Value:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		// bytewise (ordinal) order, matching BencodeNET's SortedDictionary
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		*buf = append(*buf, 'd')
		for _, k := range keys {
			if err := encodeInto(buf, []byte(k)); err != nil {
				return err
			}
			if err := encodeInto(buf, t[k]); err != nil {
				return err
			}
		}
		*buf = append(*buf, 'e')
		return nil
	default:
		return fmt.Errorf("bencode: unsupported value type %T", v)
	}
}
