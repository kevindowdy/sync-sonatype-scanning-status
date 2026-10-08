// Package spreadsheet reads and updates the scanning workbooks (.xlsx) and the
// raw FIG Version Scans export (.csv) behind one small table abstraction.
package spreadsheet

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

// utf8BOM is the byte-order mark some exports prepend to CSV files.
const utf8BOM = "\xef\xbb\xbf"

// table is a mutable grid of string cells loaded from a file.
type table interface {
	// rows returns every row; rows may be ragged (trailing empty cells trimmed).
	rows() [][]string
	// setCell writes value at zero-based row and col, growing the row if needed.
	setCell(row, col int, value string) error
	// save persists the table back to its source file atomically.
	save() error
	// close releases any resources held by the table.
	close() error
}

// openTable loads path as CSV or XLSX depending on its extension.
func openTable(path, sheetName string) (table, error) {
	// Choose the loader from the lower-cased extension.
	switch strings.ToLower(filepath.Ext(path)) {
	case ".csv":
		// CSV files have no sheets, so sheetName is ignored.
		return openCSV(path)
	case ".xlsx", ".xlsm":
		// Excel workbooks may name the sheet to use.
		return openXLSX(path, sheetName)
	default:
		// Anything else is unsupported.
		return nil, fmt.Errorf("unsupported file type %q (want .csv or .xlsx)", filepath.Ext(path))
	}
}

// writeAtomically writes data to path via a temp file and rename.
func writeAtomically(path string, write func(tmpPath string) error) error {
	// Create the temp file next to the target so rename stays on one filesystem.
	tmp, err := os.CreateTemp(filepath.Dir(path), "sync-*"+filepath.Ext(path))
	if err != nil {
		// Wrap the temp-file failure.
		return fmt.Errorf("creating temp file: %w", err)
	}
	// Remember the temp path and close the handle so writers can reopen it.
	tmpPath := tmp.Name()
	tmp.Close()
	// Remove the temp file on any failure path (no-op after a successful rename).
	defer os.Remove(tmpPath)
	// Let the caller fill the temp file.
	if err := write(tmpPath); err != nil {
		// Surface the write failure.
		return err
	}
	// Preserve the original file permissions when the target exists.
	if info, err := os.Stat(path); err == nil {
		// Best effort: a chmod failure should not lose the update.
		_ = os.Chmod(tmpPath, info.Mode().Perm())
	}
	// Swap the new file into place.
	if err := os.Rename(tmpPath, path); err != nil {
		// Wrap the rename failure.
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	// Done.
	return nil
}

// padRow grows row so index col is addressable.
func padRow(row []string, col int) []string {
	// Append empty cells until the column exists.
	for len(row) <= col {
		// Add one empty cell.
		row = append(row, "")
	}
	// Return the padded row.
	return row
}

// csvTable is a table backed by a CSV file.
type csvTable struct {
	// path is the source file.
	path string
	// data holds all records.
	data [][]string
	// bom records whether the file started with a UTF-8 BOM, to restore it on save.
	bom bool
	// crlf records whether the file used CRLF line endings, to restore them on save.
	crlf bool
}

// openCSV loads a CSV file into memory.
func openCSV(path string) (*csvTable, error) {
	// Read the whole file.
	raw, err := os.ReadFile(path)
	if err != nil {
		// Wrap the read failure.
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	// Note and strip the BOM so the first header matches by name.
	bom := bytes.HasPrefix(raw, []byte(utf8BOM))
	raw = bytes.TrimPrefix(raw, []byte(utf8BOM))
	// Parse permissively: rows may have differing field counts.
	reader := csv.NewReader(bytes.NewReader(raw))
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	// Read all records.
	data, err := reader.ReadAll()
	if err != nil {
		// Wrap the parse failure.
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	// Build the table, remembering the original formatting.
	return &csvTable{path: path, data: data, bom: bom, crlf: bytes.Contains(raw, []byte("\r\n"))}, nil
}

// rows implements table.
func (t *csvTable) rows() [][]string { return t.data }

// setCell implements table.
func (t *csvTable) setCell(row, col int, value string) error {
	// Reject out-of-range rows.
	if row < 0 || row >= len(t.data) {
		// Report the bad index.
		return fmt.Errorf("row %d out of range", row)
	}
	// Grow the row if the column is beyond its current length.
	t.data[row] = padRow(t.data[row], col)
	// Store the value.
	t.data[row][col] = value
	// Done.
	return nil
}

// save implements table.
func (t *csvTable) save() error {
	// Write through a temp file so a crash cannot truncate the export.
	return writeAtomically(t.path, func(tmpPath string) error {
		// Encode into memory first.
		var buf bytes.Buffer
		// Restore the BOM if the original had one.
		if t.bom {
			// Emit the BOM bytes.
			buf.WriteString(utf8BOM)
		}
		// Configure the writer with the original line endings.
		writer := csv.NewWriter(&buf)
		writer.UseCRLF = t.crlf
		// Write all records.
		if err := writer.WriteAll(t.data); err != nil {
			// Wrap the encode failure.
			return fmt.Errorf("encoding csv: %w", err)
		}
		// Persist the bytes to the temp file.
		return os.WriteFile(tmpPath, buf.Bytes(), 0o644)
	})
}

// close implements table.
func (t *csvTable) close() error { return nil }

// xlsxTable is a table backed by one sheet of an Excel workbook.
type xlsxTable struct {
	// path is the source workbook.
	path string
	// file is the open workbook.
	file *excelize.File
	// sheet is the worksheet name in use.
	sheet string
	// data caches the sheet rows.
	data [][]string
}

// openXLSX opens a workbook and loads one sheet (the first when sheetName is empty).
func openXLSX(path, sheetName string) (*xlsxTable, error) {
	// Open the workbook.
	f, err := excelize.OpenFile(path)
	if err != nil {
		// Wrap the open failure.
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	// Default to the first sheet.
	if sheetName == "" {
		// Ask excelize for the first sheet's name.
		sheetName = f.GetSheetName(0)
	}
	// Load the rows of the chosen sheet.
	data, err := f.GetRows(sheetName)
	if err != nil {
		// Close the workbook before failing.
		f.Close()
		// Wrap the read failure.
		return nil, fmt.Errorf("reading sheet %q of %s: %w", sheetName, path, err)
	}
	// Build the table.
	return &xlsxTable{path: path, file: f, sheet: sheetName, data: data}, nil
}

// rows implements table.
func (t *xlsxTable) rows() [][]string { return t.data }

// setCell implements table.
func (t *xlsxTable) setCell(row, col int, value string) error {
	// Reject out-of-range rows.
	if row < 0 || row >= len(t.data) {
		// Report the bad index.
		return fmt.Errorf("row %d out of range", row)
	}
	// Convert zero-based indexes to an A1-style coordinate.
	cell, err := excelize.CoordinatesToCellName(col+1, row+1)
	if err != nil {
		// Wrap the coordinate failure.
		return err
	}
	// Write the value into the workbook.
	if err := t.file.SetCellValue(t.sheet, cell, value); err != nil {
		// Wrap the write failure.
		return err
	}
	// Mirror the change in the cached rows.
	t.data[row] = padRow(t.data[row], col)
	t.data[row][col] = value
	// Done.
	return nil
}

// save implements table.
func (t *xlsxTable) save() error {
	// Write through a temp file so a crash cannot corrupt the workbook.
	return writeAtomically(t.path, func(tmpPath string) error {
		// Save the workbook to the temp path.
		return t.file.SaveAs(tmpPath)
	})
}

// close implements table.
func (t *xlsxTable) close() error { return t.file.Close() }
