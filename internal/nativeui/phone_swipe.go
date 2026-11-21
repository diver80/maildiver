package nativeui

import (
	"github.com/egoist/mygo/ui"
)

// A mail row on the phone slides aside under a finger, as in the phone's own mail app:
// to the right it runs its one leading action (read/unread) as soon as the finger lets go there;
// to the left it opens on its trailing actions (archive, delete) for a tap. One row is open at a
// time: opening another closes the last.

const phoneSwipeActionWidth = 80

// phoneSwipeAction is a button behind a mail row.
type phoneSwipeAction struct {
	label       string
	destructive bool
	run         func()
}

// phoneSwipes keeps where each row rests, by mail ID, and which one is open.
type phoneSwipes struct {
	offsets map[string]*ui.Point
	open    string
}

func (s *phoneSwipes) offset(id string) *ui.Point {
	if s.offsets == nil {
		s.offsets = map[string]*ui.Point{}
	}
	p := s.offsets[id]
	if p == nil {
		p = &ui.Point{}
		s.offsets[id] = p
	}
	return p
}

// closeAll puts every row back, as when the list shows other mails.
func (s *phoneSwipes) closeAll() {
	s.offsets, s.open = nil, ""
}

func (a *App) phoneSwipeRow(c *ui.Context, id string, leading *phoneSwipeAction, trailing []phoneSwipeAction, content func()) {
	if leading == nil && len(trailing) == 0 {
		content()
		return
	}
	t := c.Theme()
	sw := &a.phone.swipes
	off := sw.offset(id)
	box := ui.Grid(c).Columns(1).Clip().FillWidth()
	box.Children(func() {
		lead := float32(0)
		if leading != nil {
			lead = phoneSwipeActionWidth
		}
		trail := float32(len(trailing)) * phoneSwipeActionWidth
		button := func(key int, act phoneSwipeAction, bg ui.Color) {
			b := ui.Row(c.Key(key)).Width(phoneSwipeActionWidth).AlignItems(ui.Center).Justify(ui.Center).
				Background(bg).Role(ui.RoleButton).Label(act.label).Children(func() {
				ui.Text(c, act.label).TextColor(ui.RGB(255, 255, 255)).FontSize(14).TextAlign(ui.Center)
			})
			if b.Clicked() {
				*off = ui.Point{}
				sw.open = ""
				act.run()
			}
		}
		// Behind the content: the leading action fills the side it shows on, so the whole strip a
		// finger uncovers says what letting go does.
		if leading != nil {
			ui.Row(c).ColumnStart(1).RowStart(1).AlignSelf(ui.Stretch).AlignItems(ui.Stretch).JustifySelf(ui.Start).
				Children(func() { button(-1, *leading, t.Accent) })
		}
		if len(trailing) > 0 {
			ui.Row(c).ColumnStart(1).RowStart(1).AlignSelf(ui.Stretch).AlignItems(ui.Stretch).JustifySelf(ui.End).Children(func() {
				for i, act := range trailing {
					bg := t.Accent
					if act.destructive {
						bg = t.Danger
					}
					button(i, act, bg)
				}
			})
		}
		points := []ui.Point{{}}
		if trail > 0 {
			points = append(points, ui.Point{X: -trail})
		}
		if lead > 0 {
			points = append(points, ui.Point{X: lead})
		}
		face := ui.Box(c).ColumnStart(1).RowStart(1).Background(t.Background).Draggable(off, ui.DraggableOptions{
			Axis:   ui.Horizontal,
			Bounds: &ui.Rect{X: -trail, W: trail + lead},
			Snap:   ui.Nearest(points...),
		})
		face.Children(content)
		if face.Dragging() || face.Transitioning() {
			return
		}
		switch {
		case lead > 0 && off.X >= lead:
			// Let go on the leading side: run it, and the row slides back.
			*off = ui.Point{}
			if sw.open == id {
				sw.open = ""
			}
			c.Invalidate()
			leading.run()
		case off.X != 0:
			if sw.open != id {
				if prev := sw.offsets[sw.open]; prev != nil {
					*prev = ui.Point{}
					c.Invalidate() // it may have been drawn already this frame
				}
				sw.open = id
			}
		case sw.open == id:
			sw.open = ""
		}
	})
}
