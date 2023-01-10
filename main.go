package main

import (
	"embed"
	"fmt"
	"image"
	"image/color"
	_ "image/png"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/text"
	logging "github.com/tsujio/game-logging-server/client"
	"github.com/tsujio/game-util/resourceutil"
	"github.com/tsujio/game-util/touchutil"
)

const (
	gameName      = "spotted-garden-eel"
	screenWidth   = 640
	screenHeight  = 480
	screenCenterX = screenWidth / 2
	screenCenterY = screenHeight / 2
	sgeX          = screenCenterX
	seaBottom     = screenHeight - 50
)

//go:embed resources/*.ttf resources/*.png resources/*.dat resources/secret
var resources embed.FS

func loadImage(filename string) *ebiten.Image {
	f, err := resources.Open(filename)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		log.Fatal(err)
	}
	return ebiten.NewImageFromImage(img)
}

func loadAudioData(name string, audioContext *audio.Context) []byte {
	f, err := resources.Open(name)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	data, err := ioutil.ReadAll(f)
	if err != nil {
		log.Fatal(err)
	}

	return data
}

var (
	emptyImage                       = ebiten.NewImage(screenWidth, screenHeight)
	largeFont, mediumFont, smallFont = (func() (l, m, s *resourceutil.Font) {
		l, m, s, err := resourceutil.LoadFont(resources, "resources/PressStart2P-Regular.ttf", nil)
		if err != nil {
			log.Fatal(err)
		}
		return
	})()
	sgeImage      = loadImage("resources/sge.png")
	sgeHeadImages = []*ebiten.Image{
		sgeImage.SubImage(image.Rect(0, 50, 32, 90)).(*ebiten.Image),
		sgeImage.SubImage(image.Rect(50, 50, 82, 90)).(*ebiten.Image),
	}
	sgeBodyImage          = sgeImage.SubImage(image.Rect(0, 0, 25, 25)).(*ebiten.Image)
	sgeNeckImage          = sgeImage.SubImage(image.Rect(100, 0, 135, 74)).(*ebiten.Image)
	_, sgeLengthMin       = sgeNeckImage.Size()
	sgeVerticalNeckImage1 = sgeImage.SubImage(image.Rect(150, 0, 175, 34)).(*ebiten.Image)
	sgeVerticalNeckImage2 = sgeImage.SubImage(image.Rect(50, 0, 75, 25)).(*ebiten.Image)
	sgeLengthMax          = 400
	planktonImages        = (func() []*ebiten.Image {
		img := loadImage("resources/plankton.png")
		return []*ebiten.Image{
			img.SubImage(image.Rect(0, 0, 15, 15)).(*ebiten.Image),
			img.SubImage(image.Rect(15, 0, 30, 15)).(*ebiten.Image),
		}
	})()
	sunfishImages = (func() []*ebiten.Image {
		img := loadImage("resources/sunfish.png")
		return []*ebiten.Image{
			img.SubImage(image.Rect(0, 0, 50, 50)).(*ebiten.Image),
			img.SubImage(image.Rect(50, 0, 100, 50)).(*ebiten.Image),
		}
	})()
	audioContext        = audio.NewContext(48000)
	gameStartAudioData  = loadAudioData("resources/魔王魂 効果音 システム49.mp3.dat", audioContext)
	gameOverAudioData   = loadAudioData("resources/魔王魂 効果音 システム32.mp3.dat", audioContext)
	stretchAudioData    = loadAudioData("resources/魔王魂 効果音 点火02.mp3.dat", audioContext)
	eatAudioData        = loadAudioData("resources/魔王魂 効果音 物音15.mp3.dat", audioContext)
	flowChangeAudioData = loadAudioData("resources/魔王魂 効果音 スイッチ02.mp3.dat", audioContext)
)

type point struct {
	x, y float64
}

type plankton struct {
	*point
	swimVec *point
}

func (p *plankton) draw(screen *ebiten.Image, game *Game) {
	img := planktonImages[game.ticksFromModeStart/30%2]
	w, h := img.Size()
	opt := &ebiten.DrawImageOptions{}
	if game.flowVec.x < 0 {
		opt.GeoM.Scale(-1.0, 1.0)
		opt.GeoM.Translate(float64(w), 0)
	}
	opt.GeoM.Translate(p.x-float64(w)/2, p.y-float64(h)/2)
	screen.DrawImage(img, opt)
}

type sunfish struct {
	*point
	swimVec *point
}

func (s *sunfish) draw(screen *ebiten.Image, game *Game) {
	img := sunfishImages[game.ticksFromModeStart/30%2]
	w, h := img.Size()
	drawWidth, drawHeight := 100.0, 100.0
	opt := &ebiten.DrawImageOptions{}
	opt.GeoM.Scale(drawWidth/float64(w), drawHeight/float64(h))
	if game.flowVec.x < 0 {
		opt.GeoM.Scale(-1.0, 1.0)
		opt.GeoM.Translate(float64(drawWidth), 0)
	}
	opt.GeoM.Translate(s.x-float64(drawWidth)/2, s.y-float64(drawHeight)/2)
	screen.DrawImage(img, opt)
}

type flowLine struct {
	*point
	depth float64
}

type eatEffect struct {
	*point
	ticks uint
}

func (e *eatEffect) draw(screen *ebiten.Image, game *Game) {
	y := e.y - 15*math.Sin(math.Pi*float64(e.ticks)/60)
	text.Draw(screen, "+1", mediumFont.Face, int(e.x), int(y), color.RGBA{0xf5, 0xc0, 0x01, 0xff})
}

type gameMode int

const (
	gameModeTitle gameMode = iota
	gameModePlaying
	gameModeGameOver
)

type Game struct {
	playerID           string
	playID             string
	mode               gameMode
	touchContext       *touchutil.TouchContext
	isSGEStretching    bool
	flowVec            *point
	sgeLength          float64
	ticksFromModeStart uint64
	sgeEatingTicks     uint
	planktons          []plankton
	sunfishes          []sunfish
	flowLines          []flowLine
	eatEffects         []eatEffect
	score              int
}

func (g *Game) Update() error {
	g.touchContext.Update()

	g.ticksFromModeStart++

	switch g.mode {
	case gameModeTitle:
		g.sgeLength = 250

		if g.touchContext.IsJustTouched() {
			g.mode = gameModePlaying
			g.ticksFromModeStart = 0
			g.sgeLength = 0

			audio.NewPlayerFromBytes(audioContext, gameStartAudioData).Play()

			logging.LogAsync(gameName, map[string]interface{}{
				"player_id": g.playerID,
				"play_id":   g.playID,
				"action":    "start_game",
			})
		}
	case gameModePlaying:
		if g.ticksFromModeStart%600 == 0 {
			logging.LogAsync(gameName, map[string]interface{}{
				"player_id": g.playerID,
				"play_id":   g.playID,
				"action":    "playing",
				"ticks":     g.ticksFromModeStart,
				"score":     g.score,
			})
		}

		if g.sgeEatingTicks > 0 {
			g.sgeEatingTicks++
			if g.sgeEatingTicks > 60 {
				g.sgeEatingTicks = 0
			}
		}

		if g.touchContext.IsJustTouched() {
			g.isSGEStretching = true

			audio.NewPlayerFromBytes(audioContext, stretchAudioData).Play()
		}
		if g.touchContext.IsJustReleased() {
			g.isSGEStretching = false
		}

		// Flow change
		if g.ticksFromModeStart%180 == 0 && rand.Int()%2 == 0 {
			vx := float64(rand.Int()%3 + 2)
			if g.flowVec.x > 0 {
				vx *= -1
			}
			g.flowVec.x = vx

			audio.NewPlayerFromBytes(audioContext, flowChangeAudioData).Play()
		}

		// sge move
		if g.isSGEStretching {
			g.sgeLength += 3
			if g.sgeLength > float64(sgeLengthMax) {
				g.sgeLength = float64(sgeLengthMax)
			}
		} else {
			g.sgeLength -= 2
			if g.sgeLength < float64(sgeLengthMin) {
				g.sgeLength = float64(sgeLengthMin)
			}
		}

		// flowline enter
		if g.ticksFromModeStart%10 == 0 {
			var x float64
			if g.flowVec.x > 0 {
				x = -50
			} else {
				x = screenWidth + 50
			}
			f := flowLine{
				point: &point{
					x: x,
					y: float64(rand.Int() % seaBottom),
				},
				depth: float64(1 + rand.Int()%3),
			}
			g.flowLines = append(g.flowLines, f)
		}

		// Plankton and sunfish enter
		if g.ticksFromModeStart%60 == 0 {
			var x float64
			if g.flowVec.x > 0 {
				x = -50
			} else {
				x = screenWidth + 50
			}
			if rand.Int()%3 == 0 {
				g.sunfishes = append(g.sunfishes, sunfish{
					point: &point{
						x: x,
						y: float64(0 + rand.Int()%(seaBottom-sgeLengthMin-50)),
					},
					swimVec: &point{
						x: float64(rand.Int()%11-5) / 5,
						y: 0,
					},
				})
			} else {
				g.planktons = append(g.planktons, plankton{
					point: &point{
						x: x,
						y: float64(0 + rand.Int()%(seaBottom-sgeLengthMin-50)),
					},
					swimVec: &point{
						x: float64(rand.Int()%11-5) / 5,
						y: 0,
					},
				})
			}
		}

		// flowline move
		var newFlowLines []flowLine
		for i := 0; i < len(g.flowLines); i++ {
			f := &g.flowLines[i]
			f.x += g.flowVec.x / f.depth
			f.y += g.flowVec.y / f.depth

			if f.x > -50 && f.x < screenWidth+50 {
				newFlowLines = append(newFlowLines, *f)
			}
		}
		g.flowLines = newFlowLines

		// Planktons move
		var newPlanktons []plankton
		for i := 0; i < len(g.planktons); i++ {
			p := &g.planktons[i]
			p.x += g.flowVec.x + p.swimVec.x
			p.y += g.flowVec.y + p.swimVec.y

			if p.x > -50 && p.x < screenWidth+50 {
				newPlanktons = append(newPlanktons, *p)
			}
		}
		g.planktons = newPlanktons

		// Sunfishes move
		var newSunfishes []sunfish
		for i := 0; i < len(g.sunfishes); i++ {
			s := &g.sunfishes[i]
			s.x += g.flowVec.x + s.swimVec.x
			s.y += g.flowVec.y + s.swimVec.y

			if s.x > -50 && s.x < screenWidth+50 {
				newSunfishes = append(newSunfishes, *s)
			}
		}
		g.sunfishes = newSunfishes

		// Eat effect
		var newEatEffects []eatEffect
		for i := 0; i < len(g.eatEffects); i++ {
			e := &g.eatEffects[i]
			e.ticks++
			if e.ticks > 60 {
				continue
			}
			newEatEffects = append(newEatEffects, *e)
		}
		g.eatEffects = newEatEffects

		// sge and plankton collision
		newPlanktons = []plankton{}
		for i := 0; i < len(g.planktons); i++ {
			p := &g.planktons[i]
			if math.Abs(seaBottom-g.sgeLength+10-p.y) < 15 {
				var xOffset float64
				if !g.isSGEStretching {
					if g.flowVec.x > 0 {
						xOffset = -40
					} else {
						xOffset = 40
					}
				}
				if math.Abs(sgeX+xOffset-p.x) < 10 {
					g.score++
					g.sgeEatingTicks = 1
					g.eatEffects = append(g.eatEffects, eatEffect{
						point: &point{x: sgeX, y: seaBottom - g.sgeLength},
						ticks: 0,
					})

					audio.NewPlayerFromBytes(audioContext, eatAudioData).Play()

					continue
				}
			}
			newPlanktons = append(newPlanktons, *p)
		}
		g.planktons = newPlanktons

		// sge and sunfish collision
		for i := 0; i < len(g.sunfishes); i++ {
			s := &g.sunfishes[i]
			if s.y+15 > seaBottom-g.sgeLength {
				var xOffset float64
				if g.flowVec.x > 0 {
					xOffset = -20
				} else {
					xOffset = 20
				}
				if math.Abs(sgeX+xOffset-s.x) < 30 {
					g.mode = gameModeGameOver
					g.ticksFromModeStart = 0

					audio.NewPlayerFromBytes(audioContext, gameOverAudioData).Play()

					logging.LogAsync(gameName, map[string]interface{}{
						"player_id": g.playerID,
						"play_id":   g.playID,
						"action":    "game_over",
						"ticks":     g.ticksFromModeStart,
						"score":     g.score,
					})

					break
				}
			}
		}
	case gameModeGameOver:
		if g.touchContext.IsJustTouched() {
			g.initialize()
		}
	}

	return nil
}

func (g *Game) drawSeaBottom(screen *ebiten.Image, rect image.Rectangle) {
	seaBottomImage := emptyImage.SubImage(image.Rect(0, 0, rect.Dx(), rect.Dy())).(*ebiten.Image)
	seaBottomImage.Fill(color.RGBA{0xe1, 0xdc, 0xb5, 0xff})
	opt := &ebiten.DrawImageOptions{}
	opt.GeoM.Translate(float64(rect.Min.X), float64(rect.Min.Y))
	screen.DrawImage(seaBottomImage, opt)
	sandImage := emptyImage.SubImage(image.Rect(0, 0, 3, 1)).(*ebiten.Image)
	sandImage.Fill(color.RGBA{0xf5, 0xba, 0x6d, 0xff})
	for x := -50; x < rect.Dx()+50; x += 30 {
		for y := 10; y < rect.Dy(); y += 10 {
			opt := &ebiten.DrawImageOptions{}
			opt.GeoM.Translate(float64(rect.Min.X+x+y), float64(rect.Min.Y+y))
			screen.DrawImage(sandImage, opt)
		}
	}
}

func (g *Game) drawFlow(screen *ebiten.Image) {
	img := emptyImage.SubImage(image.Rect(0, 0, 30, 1)).(*ebiten.Image)
	img.Fill(color.RGBA{0xea, 0xf2, 0xff, 0xff})

	for i := 0; i < len(g.flowLines); i++ {
		f := &g.flowLines[i]
		opt := &ebiten.DrawImageOptions{}
		opt.GeoM.Scale(1.0/f.depth, 1.0)
		opt.GeoM.Translate(f.x, f.y)
		screen.DrawImage(img, opt)
	}
}

func (g *Game) drawSGE(screen *ebiten.Image) {
	bodyWidth, bodyHeight := sgeBodyImage.Size()
	var bodyStartOffset float64

	if g.isSGEStretching && g.sgeLength < float64(sgeLengthMax) {
		// Head
		var headImage *ebiten.Image
		if g.sgeEatingTicks > 0 {
			headImage = sgeHeadImages[g.sgeEatingTicks/10%2]
		} else {
			headImage = sgeHeadImages[0]
		}
		_, headHeight := headImage.Size()
		opt := &ebiten.DrawImageOptions{}
		opt.GeoM.Translate(-4, 0)
		if g.flowVec.x > 0 {
			opt.GeoM.Scale(-1.0, 1.0)
			opt.GeoM.Translate(float64(bodyWidth), 0)
		}
		opt.GeoM.Translate(float64(screenCenterX-bodyWidth/2), float64(seaBottom)-g.sgeLength)
		screen.DrawImage(headImage, opt)

		// Neck
		neckWidth, neckHeight := sgeVerticalNeckImage1.Size()
		opt.GeoM.Reset()
		if g.flowVec.x > 0 {
			opt.GeoM.Scale(-1.0, 1.0)
			opt.GeoM.Translate(float64(neckWidth), 0)
		}
		opt.GeoM.Translate(float64(screenCenterX-bodyWidth/2), float64(seaBottom)-g.sgeLength+float64(headHeight))
		screen.DrawImage(sgeVerticalNeckImage1, opt)

		bodyStartOffset = float64(headHeight + neckHeight)
	} else {
		// Neck
		neckWidth, neckHeight := sgeNeckImage.Size()
		opt := &ebiten.DrawImageOptions{}
		if g.flowVec.x > 0 {
			opt.GeoM.Scale(-1.0, 1.0)
			opt.GeoM.Translate(float64(bodyWidth), 0)
		}
		opt.GeoM.Translate(float64(screenCenterX-bodyWidth/2), float64(seaBottom)-g.sgeLength)
		screen.DrawImage(sgeNeckImage, opt)

		// Head
		var headImage *ebiten.Image
		if g.sgeEatingTicks > 0 {
			headImage = sgeHeadImages[g.sgeEatingTicks/10%2]
		} else {
			headImage = sgeHeadImages[0]
		}
		_, headHeight := headImage.Size()
		opt.GeoM.Reset()
		opt.GeoM.Rotate(math.Pi / 2)
		if g.flowVec.x > 0 {
			opt.GeoM.Scale(-1.0, 1.0)
			opt.GeoM.Translate(float64(-headHeight-neckWidth+bodyWidth), 0)
		} else {
			opt.GeoM.Translate(float64(headHeight+neckWidth), 0)
		}
		opt.GeoM.Translate(float64(screenCenterX-bodyWidth/2), float64(seaBottom)-4-g.sgeLength)
		screen.DrawImage(headImage, opt)

		bodyStartOffset = float64(neckHeight)
	}

	// Body
	var bodyDrawnLength float64
	i := 0
	for g.sgeLength > float64(bodyDrawnLength)+bodyStartOffset {
		var image *ebiten.Image
		if g.isSGEStretching && g.sgeLength < float64(sgeLengthMax) && i == 1 {
			image = sgeVerticalNeckImage2
		} else {
			image = sgeBodyImage
		}

		opt := &ebiten.DrawImageOptions{}
		if g.flowVec.x > 0 {
			opt.GeoM.Scale(-1.0, 1.0)
			opt.GeoM.Translate(float64(bodyWidth), 0)
		}
		opt.GeoM.Translate(float64(screenCenterX-bodyWidth/2), float64(seaBottom)+bodyDrawnLength+bodyStartOffset-g.sgeLength)
		screen.DrawImage(image, opt)

		bodyDrawnLength += float64(bodyHeight)
		i++
	}

	g.drawSeaBottom(screen, image.Rect(0, seaBottom, screenWidth, screenHeight))
}

func (g *Game) drawPlanktons(screen *ebiten.Image) {
	for i := 0; i < len(g.planktons); i++ {
		g.planktons[i].draw(screen, g)
	}
}

func (g *Game) drawSunfishes(screen *ebiten.Image) {
	for i := 0; i < len(g.sunfishes); i++ {
		g.sunfishes[i].draw(screen, g)
	}
}

func (g *Game) drawEatEffects(screen *ebiten.Image) {
	for i := 0; i < len(g.eatEffects); i++ {
		g.eatEffects[i].draw(screen, g)
	}
}

func (g *Game) drawTitle(screen *ebiten.Image) {
	titleText := []string{"SPOTTED", "GARDEN", "EEL"}
	for i, s := range titleText {
		text.Draw(screen, s, largeFont.Face, 40, 85+i*int(largeFont.FaceOptions.Size*1.8), color.White)
	}

	creditTexts := []string{"CREATOR: NAOKI TSUJIO", "FONT: Press Start 2P", "by CodeMan38", "SOUND: MaouDamashii"}
	for i, s := range creditTexts {
		text.Draw(screen, s, smallFont.Face, screenWidth-len(s)*int(smallFont.FaceOptions.Size)-10, 320+i*int(smallFont.FaceOptions.Size*1.8), color.White)
	}
}

func (g *Game) drawScore(screen *ebiten.Image) {
	scoreText := fmt.Sprintf("SCORE %d", g.score)
	text.Draw(screen, scoreText, smallFont.Face, screenWidth-len(scoreText)*int(smallFont.FaceOptions.Size)-10, 20, color.White)
}

func (g *Game) drawGameOver(screen *ebiten.Image) {
	gameOverText := "GAME OVER"
	text.Draw(screen, gameOverText, largeFont.Face, screenCenterX-len(gameOverText)*int(largeFont.FaceOptions.Size)/2, 200, color.White)
	scoreText := []string{"YOU ATE", fmt.Sprintf("%d PLANKTONS!", g.score)}
	for i, s := range scoreText {
		text.Draw(screen, s, mediumFont.Face, screenCenterX-len(s)*int(mediumFont.FaceOptions.Size)/2, 260+i*int(mediumFont.FaceOptions.Size*2), color.White)
	}
}

func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(color.RGBA{0x94, 0xd5, 0xf5, 0xff})

	g.drawFlow(screen)

	g.drawSeaBottom(screen, image.Rect(0, seaBottom-30, screenWidth, screenHeight))

	switch g.mode {
	case gameModeTitle:
		g.drawTitle(screen)
		g.drawSGE(screen)
	case gameModePlaying:
		g.drawSGE(screen)
		g.drawPlanktons(screen)
		g.drawSunfishes(screen)
		g.drawEatEffects(screen)
		g.drawScore(screen)
	case gameModeGameOver:
		g.drawSGE(screen)
		g.drawPlanktons(screen)
		g.drawSunfishes(screen)
		g.drawScore(screen)
		g.drawGameOver(screen)
	}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return screenWidth, screenHeight
}

func (g *Game) initialize() {
	logging.LogAsync(gameName, map[string]interface{}{
		"player_id": g.playerID,
		"play_id":   g.playID,
		"action":    "initialize",
	})

	g.mode = gameModeTitle
	g.ticksFromModeStart = 0
	g.sgeLength = float64(sgeLengthMin)
	g.isSGEStretching = false
	g.sgeEatingTicks = 0
	g.flowVec = &point{x: 2, y: 0}
	g.planktons = []plankton{}
	g.sunfishes = []sunfish{}
	g.score = 0

	// flowline
	var flowLines []flowLine
	for tick := 0; tick < 30*60; tick++ {
		if tick%10 == 0 {
			f := flowLine{
				point: &point{
					x: -50,
					y: float64(rand.Int() % seaBottom),
				},
				depth: float64(1 + rand.Int()%3),
			}
			flowLines = append(flowLines, f)
		}
		for i := 0; i < len(flowLines); i++ {
			f := &flowLines[i]
			f.x += g.flowVec.x / f.depth
			f.y += g.flowVec.y / f.depth
		}
	}
	g.flowLines = flowLines
}

func main() {
	if os.Getenv("GAME_LOGGING") == "1" {
		secret, err := resources.ReadFile("resources/secret")
		if err == nil {
			logging.Enable(string(secret))
		}
	} else {
		logging.Disable()
	}

	if seed, err := strconv.Atoi(os.Getenv("GAME_RAND_SEED")); err == nil {
		rand.Seed(int64(seed))
	} else {
		rand.Seed(time.Now().Unix())
	}
	playerID := os.Getenv("GAME_PLAYER_ID")
	if playerID == "" {
		if playerIDObj, err := uuid.NewRandom(); err == nil {
			playerID = playerIDObj.String()
		}
	}

	ebiten.SetWindowSize(screenWidth, screenHeight)
	ebiten.SetWindowTitle("Spotted Garden Eel")

	playIDObj, err := uuid.NewRandom()
	var playID string
	if err != nil {
		playID = "?"
	} else {
		playID = playIDObj.String()
	}

	game := &Game{
		playerID:     playerID,
		playID:       playID,
		touchContext: touchutil.CreateTouchContext(),
	}
	game.initialize()

	if err := ebiten.RunGame(game); err != nil {
		log.Fatal(err)
	}
}
