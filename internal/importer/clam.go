package importer

import (
	"context"
	"time"

	"fdt/internal/av"
)

// ClamScanner is the production import scanner. Executable extensions are
// rejected locally; file bytes are streamed to clamd. A clamd outage returns
// an error so the import stays in quarantine (fail closed).
type ClamScanner struct {
	Clam     *av.Client
	BlockExt []string
}

func (s ClamScanner) Scan(ctx context.Context, dir string) (ScanResult, error) {
	if s.Clam == nil {
		return ScanResult{}, errNoClamd
	}
	rules := RuleBasedScanner{BlockExt: s.BlockExt}.Scan
	local, err := rules(ctx, dir)
	if err != nil {
		return ScanResult{}, err
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	sigs, err := s.Clam.ScanDir(cctx, dir)
	if err != nil {
		return ScanResult{}, err
	}
	res := ScanResult{
		Clean:      local.Clean && len(sigs) == 0,
		Findings:   append([]string{}, local.Findings...),
		Categories: local.Categories,
		Engine:     "clamd",
	}
	if res.Categories == nil {
		res.Categories = map[string]int{}
	}
	for _, sig := range sigs {
		res.Findings = append(res.Findings, "病毒: "+sig)
		res.Categories["virus"]++
	}
	// RuleBasedScanner names itself in Engine and treats EICAR as a hit, which
	// is correct alongside clamd. Drop the dev engine label.
	return res, nil
}
