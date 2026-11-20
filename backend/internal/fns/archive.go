package fns

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"
)

func DownloadZIP(ctx context.Context, url string, maxBytes int64, timeout time.Duration) (*os.File, error) {
	client := &http.Client{Timeout: timeout}
	for attempt := 1; ; attempt++ {
		file, retry, err := download(ctx, client, url, maxBytes)
		if !retry || attempt == 3 {
			return file, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}
}

func download(ctx context.Context, client *http.Client, url string, maxBytes int64) (*os.File, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, ctx.Err() == nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode >= 500 && resp.Header.Get("Retry-After") == "", fmt.Errorf("FNS returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return nil, false, fmt.Errorf("ZIP exceeds %d bytes", maxBytes)
	}
	file, err := os.CreateTemp("", "company-search-fns-*.part")
	if err != nil {
		return nil, false, err
	}
	size, err := io.Copy(file, io.LimitReader(resp.Body, maxBytes+1))
	if err == nil && size > 0 && size <= maxBytes {
		return file, false, nil
	}
	file.Close()
	os.Remove(file.Name())
	if err != nil {
		return nil, ctx.Err() == nil && err == io.ErrUnexpectedEOF, err
	}
	return nil, false, fmt.Errorf("unexpected ZIP size: %d", size)
}

func readXMLArchive(ctx context.Context, z *zip.Reader, maxEntries int, maxXML int64, maxTotal uint64, read func(io.Reader, string) error) error {
	if len(z.File) == 0 || len(z.File) > maxEntries {
		return fmt.Errorf("unexpected number of ZIP entries: %d", len(z.File))
	}
	var total uint64
	names := make(map[string]bool, len(z.File))
	for _, entry := range z.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !strings.EqualFold(path.Ext(entry.Name), ".xml") || !entry.Mode().IsRegular() || names[entry.Name] ||
			entry.UncompressedSize64 > uint64(maxXML) || entry.UncompressedSize64 == 0 {
			return fmt.Errorf("unexpected ZIP entry %q", entry.Name)
		}
		names[entry.Name] = true
		total += entry.UncompressedSize64
		if total > maxTotal {
			return fmt.Errorf("uncompressed ZIP exceeds size limit")
		}
		r, err := entry.Open()
		if err != nil {
			return err
		}
		limited := &io.LimitedReader{R: r, N: maxXML + 1}
		err = read(limited, entry.Name)
		r.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", entry.Name, err)
		}
		if limited.N == 0 {
			return fmt.Errorf("XML exceeds size limit")
		}
	}
	return nil
}

func readFNSXML(ctx context.Context, r io.Reader, name, version, info string, requireSender bool, read func(*xml.Decoder, xml.StartElement) error) error {
	d := xml.NewDecoder(r)
	d.CharsetReader = func(charset string, in io.Reader) (io.Reader, error) {
		if strings.EqualFold(charset, "windows-1251") {
			return charmap.Windows1251.NewDecoder().Reader(in), nil
		}
		return nil, fmt.Errorf("unsupported XML encoding %q", charset)
	}
	var expected, seen int
	var open, closed, sender bool
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err := d.Token()
		if err == io.EOF && closed && (!requireSender || sender) && seen == expected {
			return nil
		}
		if err != nil {
			return fmt.Errorf("XML incomplete or invalid: read %d of %d documents: %w", seen, expected, err)
		}
		switch t := token.(type) {
		case xml.StartElement:
			if t.Name.Space != "" || closed {
				return fmt.Errorf("unexpected element %s", t.Name.Local)
			}
			switch t.Name.Local {
			case "Файл":
				if open || attr(t, "ВерсФорм") != version || attr(t, "ТипИнф") != info ||
					!strings.EqualFold(attr(t, "ИдФайл"), strings.TrimSuffix(path.Base(name), path.Ext(name))) {
					return fmt.Errorf("unexpected FNS XML root metadata or format version")
				}
				expected, err = strconv.Atoi(attr(t, "КолДок"))
				if err != nil || expected <= 0 {
					return fmt.Errorf("invalid КолДок")
				}
				open = true
			case "ИдОтпр":
				if !open || sender || seen > 0 {
					return fmt.Errorf("unexpected sender")
				}
				if err := d.Skip(); err != nil {
					return err
				}
				sender = true
			case "Документ":
				if !open || (requireSender && !sender) || seen >= expected {
					return fmt.Errorf("unexpected document")
				}
				if err := read(d, t); err != nil {
					return err
				}
				seen++
			default:
				return fmt.Errorf("unexpected element %s", t.Name.Local)
			}
		case xml.EndElement:
			closed = true
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" {
				return fmt.Errorf("unexpected XML text")
			}
		case xml.Directive:
			return fmt.Errorf("unexpected XML directive")
		}
	}
}

func attr(element xml.StartElement, name string) string {
	for _, a := range element.Attr {
		if a.Name.Space == "" && a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
