package wodbuster

import (
	"encoding/json"
	"time"
)

// The shapes WodBuster actually sends. Nothing here escapes the package: the
// Spanish field names, the ticks and the state strings all stop at this file.

type wireDay struct {
	Title                    string  `json:"Title"`
	Version                  int64   `json:"Version"`
	SegundosHastaPublicacion float64 `json:"SegundosHastaPublicacion"`
	TipoNoClases             string  `json:"TipoNoClases"`
	Data                     []struct {
		Hora    string `json:"Hora"`
		Valores []struct {
			TipoEstado string `json:"TipoEstado"`
			Valor      struct {
				Id                int64             `json:"Id"`
				Nombre            string            `json:"Nombre"`
				Plazas            int               `json:"Plazas"`
				HoraComienzo      string            `json:"HoraComienzo"`
				AtletasEntrenando []json.RawMessage `json:"AtletasEntrenando"`
			} `json:"Valor"`
		} `json:"Valores"`
	} `json:"Data"`
}

func (w wireDay) toSchedule(d Date) Schedule {
	s := Schedule{Date: d, Published: len(w.Data) > 0}

	// The counter is only meaningful while it is still in the future; once the
	// day is out the server keeps counting and it goes negative.
	if w.SegundosHastaPublicacion > 0 {
		s.OpensIn = time.Duration(w.SegundosHastaPublicacion * float64(time.Second))
	}

	for _, block := range w.Data {
		for _, v := range block.Valores {
			start, err := ParseTimeOfDay(v.Valor.HoraComienzo)
			if err != nil {
				// Fall back to the block's own hour; if that is unreadable too,
				// keep the class with a zero time rather than dropping it.
				start, _ = ParseTimeOfDay(block.Hora)
			}
			s.Classes = append(s.Classes, Class{
				ID:       ClassID(v.Valor.Id),
				Name:     v.Valor.Nombre,
				Date:     d,
				Start:    start,
				Capacity: v.Valor.Plazas,
				Booked:   len(v.Valor.AtletasEntrenando),
				State:    parseState(v.TipoEstado),
				raw:      v.TipoEstado,
			})
		}
	}
	return s
}

// wireAction is the answer to Inscribir / Avisar / Borrar.
//
// The body is the whole refreshed day — the same payload LoadClass returns —
// and the verdict on what we just asked for sits in one small object under
// "Res". Reading the verdict at the top level finds nothing, which decodes as
// a rejection with no message: a booking that worked then reads as a failure.
// Observed against firespain on 2026-09-20; before that this type was a guess.
type wireAction struct {
	Res *wireResult `json:"Res"`

	// Embedded, so a body that ever does carry the verdict at the top level
	// still parses. Res wins when both are there.
	wireResult
}

type wireResult struct {
	EsCorrecto       bool   `json:"EsCorrecto"`
	ErrorMsg         string `json:"ErrorMsg"`
	Error            string `json:"Error"`
	NeedAdminConfirm bool   `json:"NeedAdminConfirm"`
}

func (w wireAction) result() wireResult {
	if w.Res != nil {
		return *w.Res
	}
	return w.wireResult
}

func (w wireResult) message() string {
	switch {
	case w.ErrorMsg != "":
		return w.ErrorMsg
	case w.Error != "":
		return w.Error
	default:
		return ""
	}
}
