package main

import (
	"bytes"
	"encoding/csv"

	"github.com/xuri/excelize/v2"
)

// toCSV writes rows as CSV readable by Excel in a Russian locale: UTF-8 BOM, ';' separator.
func toCSV(rows [][]string) []byte {
	var b bytes.Buffer
	b.WriteString("\uFEFF")
	w := csv.NewWriter(&b)
	w.Comma = ';'
	w.UseCRLF = true
	_ = w.WriteAll(rows)
	return b.Bytes()
}

type sheet struct {
	Name string
	Rows [][]any
}

// toXLSX builds an Excel workbook; the first row of every sheet is a bold, frozen header.
func toXLSX(sheets []sheet) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()
	bold, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}, Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"DCE6F1"}}})
	for i, sh := range sheets {
		name := sh.Name
		if len([]rune(name)) > 31 {
			name = string([]rune(name)[:31])
		}
		if i == 0 {
			if err := f.SetSheetName("Sheet1", name); err != nil {
				return nil, err
			}
		} else if _, err := f.NewSheet(name); err != nil {
			return nil, err
		}
		width := 0
		for r, row := range sh.Rows {
			cell, _ := excelize.CoordinatesToCellName(1, r+1)
			if err := f.SetSheetRow(name, cell, &row); err != nil {
				return nil, err
			}
			width = max(width, len(row))
		}
		if width > 0 && len(sh.Rows) > 0 {
			end, _ := excelize.CoordinatesToCellName(width, 1)
			_ = f.SetCellStyle(name, "A1", end, bold)
			last, _ := excelize.ColumnNumberToName(width)
			_ = f.SetColWidth(name, "A", last, 18)
			_ = f.SetPanes(name, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
		}
	}
	var b bytes.Buffer
	if err := f.Write(&b); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
