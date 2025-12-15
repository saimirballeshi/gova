package main

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"log"
	"os"
	"strings"

	"gioui.org/app"
	"gioui.org/font/gofont"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

// ==========================================
// 1. THEME & STYLING (The "Nova" Look)
// ==========================================

var (
	BgMain    = color.NRGBA{R: 243, G: 244, B: 246, A: 255} // Light Gray
	BgSidebar = color.NRGBA{R: 30, G: 41, B: 59, A: 255}    // Dark Slate
	BgCard    = color.NRGBA{R: 255, G: 255, B: 255, A: 255} // White
	NovaBlue  = color.NRGBA{R: 64, G: 153, B: 222, A: 255}  // Blue
	TextMain  = color.NRGBA{R: 55, G: 65, B: 81, A: 255}    // Dark Gray
	BorderCol = color.NRGBA{R: 229, G: 231, B: 235, A: 255} // Light Border
)

// Helper: Draw a White Card with Rounded Corners
func DrawCard(gtx layout.Context, content layout.Widget) layout.Dimensions {
	return layout.UniformInset(unit.Dp(0)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// Draw Shadow/Border
		border := widget.Border{Color: BorderCol, CornerRadius: unit.Dp(8), Width: unit.Dp(1)}
		return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			// Clip Content
			defer clip.RRect{
				Rect: image.Rectangle{Max: gtx.Constraints.Min},
				SE:   gtx.Dp(8), SW: gtx.Dp(8), NW: gtx.Dp(8), NE: gtx.Dp(8),
			}.Push(gtx.Ops).Pop()
			
			paint.Fill(gtx.Ops, BgCard)
			return layout.UniformInset(unit.Dp(0)).Layout(gtx, content)
		})
	})
}

// ==========================================
// 2. CORE INTERFACES
// ==========================================

type Resource interface {
	Label() string
	Fields() []Field
}

type Field interface {
	Name() string
	Attribute() string
	Layout(gtx layout.Context, th *material.Theme) layout.Dimensions
	SetText(txt string)
	Value() string
}

// ==========================================
// 3. DATABASE LAYER (Neo4j)
// ==========================================

type Repository struct {
	Driver neo4j.DriverWithContext
}

func NewRepository(uri, username, password string) *Repository {
	driver, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(username, password, ""))
	if err != nil {
		log.Fatal(err)
	}
	return &Repository{Driver: driver}
}

func (r *Repository) Index(ctx context.Context, res Resource) ([]*neo4j.Record, error) {
	cypher := fmt.Sprintf("MATCH (n:%s) RETURN n LIMIT 25", res.Label())
	result, err := neo4j.ExecuteQuery(ctx, r.Driver, cypher, nil, neo4j.EagerResultTransformer)
	if err != nil {
		return nil, err
	}
	return result.Records, nil
}

func (r *Repository) Store(ctx context.Context, res Resource, data map[string]interface{}) error {
	cypher := fmt.Sprintf("CREATE (n:%s) SET n = $props", res.Label())
	_, err := neo4j.ExecuteQuery(ctx, r.Driver, cypher, map[string]interface{}{"props": data}, neo4j.EagerResultTransformer)
	return err
}

// ==========================================
// 4. FIELDS & RESOURCES
// ==========================================

// TextField Implementation
type TextField struct {
	LabelStr string
	Attr     string
	Editor   widget.Editor
}
func (t *TextField) Name() string { return t.LabelStr }
func (t *TextField) Attribute() string { return t.Attr }
func (t *TextField) Value() string { return t.Editor.Text() }
func (t *TextField) SetText(txt string) { t.Editor.SetText(txt) }

func (t *TextField) Layout(gtx layout.Context, th *material.Theme) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Body2(th, t.LabelStr)
			l.Color = TextMain
			l.Font.Weight = text.Bold
			return l.Layout(gtx)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			border := widget.Border{Color: BorderCol, CornerRadius: unit.Dp(4), Width: unit.Dp(1)}
			return border.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(10)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					ed := material.Editor(th, &t.Editor, "")
					return ed.Layout(gtx)
				})
			})
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(15)}.Layout),
	)
}

// User Resource Definition
type UserResource struct{}
func (u UserResource) Label() string { return "User" }
func (u UserResource) Fields() []Field {
	return []Field{
		&TextField{LabelStr: "Full Name", Attr: "name", Editor: widget.Editor{SingleLine: true}},
		&TextField{LabelStr: "Email Address", Attr: "email", Editor: widget.Editor{SingleLine: true}},
	}
}

// ==========================================
// 5. APPLICATION STATE & UI
// ==========================================

type App struct {
	Repo       *Repository
	Theme      *material.Theme
	Resources  []Resource
	CurrentRes Resource
	
	// State
	View       string // "index", "create"
	CachedData []*neo4j.Record
	
	// Widgets
	NavList    widget.List
	NavButtons []*widget.Clickable
	TableList  widget.List
	CreateBtn  widget.Clickable
	SaveBtn    widget.Clickable
	
	Window     *app.Window
}

func (a *App) Layout(gtx layout.Context) layout.Dimensions {
	// 1. Paint Background
	paint.FillShape(gtx.Ops, BgMain, clip.Rect{Max: gtx.Constraints.Max}.Op())

	return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
		// Sidebar
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return a.renderSidebar(gtx)
		}),
		// Content
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(30)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				if a.View == "create" {
					return a.renderForm(gtx)
				}
				return a.renderTable(gtx)
			})
		}),
	)
}

func (a *App) renderSidebar(gtx layout.Context) layout.Dimensions {
	gtx.Constraints.Min.X = gtx.Dp(250)
	gtx.Constraints.Max.X = gtx.Dp(250)
	paint.FillShape(gtx.Ops, BgSidebar, clip.Rect{Max: gtx.Constraints.Max}.Op())
	
	return layout.Inset{Top: unit.Dp(30)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.List(a.Theme, &a.NavList).Layout(gtx, len(a.Resources), func(gtx layout.Context, i int) layout.Dimensions {
			if a.NavButtons[i].Clicked(gtx) {
				a.CurrentRes = a.Resources[i]
				a.View = "index"
				a.fetchData()
			}
			
			// Custom Sidebar Button
			return material.Clickable(gtx, a.NavButtons[i], func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(15), Bottom: unit.Dp(15), Left: unit.Dp(20)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					l := material.Body1(a.Theme, a.Resources[i].Label())
					l.Color = color.NRGBA{200, 200, 200, 255}
					if a.CurrentRes == a.Resources[i] {
						l.Color = NovaBlue
						l.Font.Weight = text.Bold
					}
					return l.Layout(gtx)
				})
			})
		})
	})
}

func (a *App) renderTable(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		// Header Area
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Spacing: layout.SpaceBetween}.Layout(gtx,
				layout.Rigid(material.H5(a.Theme, a.CurrentRes.Label()).Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if a.CreateBtn.Clicked(gtx) {
						a.View = "create"
					}
					btn := material.Button(a.Theme, &a.CreateBtn, "Create New")
					btn.Background = NovaBlue
					return btn.Layout(gtx)
				}),
			)
		}),
		layout.Rigid(layout.Spacer{Height: unit.Dp(20)}.Layout),
		// Table Card
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return DrawCard(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.List(a.Theme, &a.TableList).Layout(gtx, len(a.CachedData), func(gtx layout.Context, i int) layout.Dimensions {
					// Extract Node Props
					node := a.CachedData[i].Values[0].(neo4j.Node)
					
					// Row Layout
					return layout.Stack{}.Layout(gtx,
						layout.Expanded(func(gtx layout.Context) layout.Dimensions {
							// Bottom Border
							rect := clip.Rect{Min: image.Point{0, gtx.Constraints.Min.Y - 1}, Max: gtx.Constraints.Min}
							paint.FillShape(gtx.Ops, BorderCol, rect.Op())
							return layout.Dimensions{}
						}),
						layout.Stacked(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Top: unit.Dp(15), Bottom: unit.Dp(15), Left: unit.Dp(20)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								// Display first prop found for demo
								txt := "Node"
								for _, f := range a.CurrentRes.Fields() {
									if val, ok := node.Props[f.Attribute()]; ok {
										txt = fmt.Sprintf("%v", val)
										break
									}
								}
								return material.Body1(a.Theme, txt).Layout(gtx)
							})
						}),
					)
				})
			})
		}),
	)
}

func (a *App) renderForm(gtx layout.Context) layout.Dimensions {
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return DrawCard(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(400) // Fixed Width Form
			return layout.Inset{Top: unit.Dp(30), Bottom: unit.Dp(30), Left: unit.Dp(30), Right: unit.Dp(30)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				
				var kids []layout.FlexChild
				kids = append(kids, layout.Rigid(material.H6(a.Theme, "Create "+a.CurrentRes.Label()).Layout))
				kids = append(kids, layout.Rigid(layout.Spacer{Height: unit.Dp(20)}.Layout))
				
				// Render Fields
				for _, f := range a.CurrentRes.Fields() {
					fieldWidget := f // Capture closure
					kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return fieldWidget.Layout(gtx, a.Theme)
					}))
				}
				
				// Save Button
				kids = append(kids, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if a.SaveBtn.Clicked(gtx) {
						go func() {
							data := make(map[string]interface{})
							for _, f := range a.CurrentRes.Fields() {
								data[f.Attribute()] = f.Value()
							}
							a.Repo.Store(context.Background(), a.CurrentRes, data)
							a.View = "index"
							a.fetchData() // Refresh
							a.Window.Invalidate()
						}()
					}
					btn := material.Button(a.Theme, &a.SaveBtn, "Save Resource")
					btn.Background = NovaBlue
					return btn.Layout(gtx)
				}))

				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, kids...)
			})
		})
	})
}

// Background Data Fetcher
func (a *App) fetchData() {
	go func() {
		data, _ := a.Repo.Index(context.Background(), a.CurrentRes)
		a.CachedData = data
		a.Window.Invalidate()
	}()
}

// ==========================================
// 6. MAIN ENTRY
// ==========================================

func main() {
	// Setup Database (Change credentials to match your local Neo4j)
	repo := NewRepository("neo4j+s://c46cdfa4.databases.neo4j.io", "neo4j", "bs5GPhugcnWvMaD39WD29QSzSx9jnhZwcQRfthW75hg")

	// Setup UI
	w := app.NewWindow(app.Title("Gova Admin"), app.Size(unit.Dp(1024), unit.Dp(768)))
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))

	// Init Resources
	resources := []Resource{UserResource{}}
	
	// Init Application State
	application := &App{
		Repo:       repo,
		Theme:      th,
		Resources:  resources,
		CurrentRes: resources[0],
		View:       "index",
		Window:     w,
		NavButtons: make([]*widget.Clickable, len(resources)),
	}
	for i := range application.NavButtons { application.NavButtons[i] = &widget.Clickable{} }

	// Initial Data Load
	application.fetchData()

	// Main Loop
	go func() {
		for {
			e := <-w.Events()
			switch e := e.(type) {
			case system.DestroyEvent:
				os.Exit(0)
			case system.FrameEvent:
				gtx := layout.NewContext(&op.Ops{}, e)
				application.Layout(gtx)
				e.Frame(gtx.Ops)
			}
		}
	}()

	app.Main()
}
