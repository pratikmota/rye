//go:build !b_norepl && !wasm && !js

package evaldo

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/eiannone/keyboard"

	"github.com/refaktor/rye/env"
	"github.com/refaktor/rye/loader"
	"github.com/refaktor/rye/util"

	"github.com/refaktor/liner"
)

var (
	history_fn = filepath.Join(os.TempDir(), ".rye_repl_history")
	names      = []string{"add", "join", "return", "fn", "fail", "if"}
)

type ShellEd struct {
	CurrObj env.Function
	Pause   bool
	Askfor  []string
	Mode    string
	Return  env.Object
}

func genPrompt(shellEd *ShellEd, line string) (string, string) {
	if shellEd != nil && shellEd.Mode != "" {
		a := shellEd.Askfor
		if len(a) > 0 {
			x := a[0]
			a = a[1:]
			shellEd.Askfor = a
			return "{ Rye - value of " + x + " } ", x
		} else if shellEd.Return == nil {
			return "{ Rye - expected return value } ", "<-return->"
		}
		return "{ Rye " + shellEd.Mode + "} ", ""
	} else {
		if len(line) > 0 {
			return "        ", ""
		} else {
			return "×> ", ""
		}
	}
}

func maybeDoShedCommands(line string, es *env.ProgramState, shellEd *ShellEd) {
	//fmt.Println(shellEd)
	line1 := strings.Split(line, " ")
	block := shellEd.CurrObj.Body
	switch line1[0] {
	case "#ra":
		//es.Res.Trace("ADD1")
		block.Series.Append(es.Res)
		//es.Res.Trace("ADD2")
	case "#in":
		//fmt.Println("in")
		//es.Res.Trace("*es.Idx")
		//b := es.Res.(env.Block)
		//fmt.Println(es.Res)
		shellEd.Mode = "fn"
		fn := es.Res.(env.Function)
		words := fn.Spec.Series.GetAll()
		for _, x := range words {
			shellEd.Askfor = append(shellEd.Askfor, es.Idx.GetWord(x.(env.Word).Index))
		}
		shellEd.CurrObj = es.Res.(env.Function)
		//fmt.Println(shellEd)
	case "#ls":
		fmt.Println(shellEd.CurrObj.Inspect(*es.Idx))
	case "#s":
		i := es.Idx.IndexWord(line1[1])
		es.Ctx.Set(i, shellEd.CurrObj)
	case "#.":
		shellEd.Pause = true
	case "#>":
		shellEd.Pause = false
	case "#out":
		shellEd.Mode = ""
	}
}

func maybeDoShedCommandsBlk(line string, es *env.ProgramState, block *env.Block, shed_pause *bool) {
	//if block != nil {
	//block.Trace("TOP")
	//}
	line1 := strings.Split(line, " ")
	switch line1[0] {
	case "#in":
		//fmt.Println("in")
		//es.Res.Trace("*es.Idx")
		//b := es.Res.(env.Block)
		*block = es.Res.(env.Block)
		//block.Trace("*es.Idx")
	case "#ls":
		fmt.Println(block.Inspect(*es.Idx))
	case "#ra":
		//es.Res.Trace("ADD1")
		block.Series.Append(es.Res)
		//es.Res.Trace("ADD2")
	case "#s":
		i := es.Idx.IndexWord(line1[1])
		es.Ctx.Set(i, *block)
	case "#,":
		*shed_pause = true
	case "#>":
		*shed_pause = false
	case "#out":
		block = nil
	}
}

// terminal commands from buger/goterm

// Clear screen
func Clear() {
	fmt.Print("\033[2J")
}

// Move cursor to given position
func MoveCursor(x int, y int) {
	fmt.Printf("\033[%d;%dH", y, x)
}

// Move cursor up relative the current position
func MoveCursorUp(bias int) {
	fmt.Printf("\033[%dA", bias)
}

// Move cursor down relative the current position
func MoveCursorDown(bias int) {
	fmt.Printf("\033[%dB", bias)
}

// Move cursor forward relative the current position
func MoveCursorForward(bias int) {
	fmt.Printf("\033[%dC", bias)
}

// Move cursor backward relative the current position
func MoveCursorBackward(bias int) {
	fmt.Printf("\033[%dD", bias)
}

type Repl struct {
	ps *env.ProgramState
	ml *util.MLState

	dialect     string
	showResults bool

	fullCode string

	stack      *EyrStack
	prevResult env.Object
}

func (r *Repl) recieveMessage(message string) {
	fmt.Print(message)
}

func (r *Repl) recieveLine(line string) string {
	res := r.evalLine(r.ps, line)
	if r.showResults {
		fmt.Print(res)
	}
	return res
}

func (r *Repl) evalLine(es *env.ProgramState, code string) string {
	es.LiveObj.PsMutex.Lock()
	for _, update := range es.LiveObj.Updates {
		fmt.Println("\033[35m((Reloading " + update + "))\033[0m")
		block_, script_ := LoadScriptLocalFile(es, *env.NewUri1(es.Idx, "file://"+update))
		es.Res = EvaluateLoadedValue(es, block_, script_, true)
	}
	es.LiveObj.ClearUpdates()
	es.LiveObj.PsMutex.Unlock()

	multiline := len(code) > 1 && code[len(code)-1:] == " "

	comment := regexp.MustCompile(`\s*;`)
	line := comment.Split(code, 2) //--- just very temporary solution for some comments in repl. Later should probably be part of loader ... maybe?
	lineReal := strings.Trim(line[0], "\t")

	output := ""
	if multiline {
		r.fullCode += lineReal + "\n"
	} else {
		r.fullCode += lineReal

		block, genv := loader.LoadString(r.fullCode, false)
		block1 := block.(env.Block)
		es = env.AddToProgramState(es, block1.Series, genv)

		// EVAL THE DO DIALECT
		if r.dialect == "do" {
			EvalBlockInj(es, r.prevResult, true)
		} else if r.dialect == "eyr" {
			Eyr_EvalBlock(es, r.stack, true)
		} else if r.dialect == "math" {
			idxx, _ := es.Idx.GetIndex("math")
			s1, ok := es.Ctx.Get(idxx)
			if ok {
				switch ss := s1.(type) {
				case env.RyeCtx: /*  */
					es.Ctx = &ss
					// return s1
				}
			}
			res := DialectMath(es, block1)
			switch block := res.(type) {
			case env.Block:
				stack := NewEyrStack()
				ser := es.Ser
				es.Ser = block.Series
				Eyr_EvalBlock(es, stack, false)
				es.Ser = ser
			}
		}

		MaybeDisplayFailureOrError(es, genv)

		if !es.ErrorFlag && es.Res != nil {
			r.prevResult = es.Res
			output = fmt.Sprintf("\033[38;5;37m" + es.Res.Inspect(*genv) + "\x1b[0m")
		}

		es.ReturnFlag = false
		es.ErrorFlag = false
		es.FailureFlag = false

		r.fullCode = ""
	}

	r.ml.AppendHistory(code)
	return output
}

// constructKeyEvent maps a rune and keyboard.Key to a util.KeyEvent, which uses javascript key event codes
// only keys used in microliner are mapped
func constructKeyEvent(r rune, k keyboard.Key) util.KeyEvent {
	var ctrl bool
	alt := k == keyboard.KeyEsc
	var code int
	ch := string(r)
	switch k {
	case keyboard.KeyCtrlA:
		ch = "a"
		ctrl = true
	case keyboard.KeyCtrlC:
		ch = "c"
		ctrl = true
	case keyboard.KeyCtrlB:
		ch = "b"
		ctrl = true
	case keyboard.KeyCtrlD:
		ch = "d"
		ctrl = true
	case keyboard.KeyCtrlE:
		ch = "e"
		ctrl = true
	case keyboard.KeyCtrlF:
		ch = "f"
		ctrl = true
	case keyboard.KeyCtrlK:
		ch = "k"
		ctrl = true
	case keyboard.KeyCtrlL:
		ch = "l"
		ctrl = true
	case keyboard.KeyCtrlN:
		ch = "n"
		ctrl = true
	case keyboard.KeyCtrlP:
		ch = "p"
		ctrl = true
	case keyboard.KeyCtrlU:
		ch = "u"
		ctrl = true

	case keyboard.KeyEnter:
		code = 13
	case keyboard.KeyBackspace, keyboard.KeyBackspace2:
		code = 8
	case keyboard.KeyDelete:
		code = 46
	case keyboard.KeyArrowRight:
		code = 39
	case keyboard.KeyArrowLeft:
		code = 37
	case keyboard.KeyArrowUp:
		code = 38
	case keyboard.KeyArrowDown:
		code = 40
	case keyboard.KeyHome:
		code = 36
	case keyboard.KeyEnd:
		code = 35

	case keyboard.KeySpace:
		ch = " "
	}
	return util.NewKeyEvent(ch, code, ctrl, alt, false)
}

func DoRyeRepl(es *env.ProgramState, dialect string, showResults bool) { // here because of some odd options we were experimentally adding
	err := keyboard.Open()
	if err != nil {
		fmt.Println(err)
		return
	}

	c := make(chan util.KeyEvent)
	r := Repl{
		ps:          es,
		dialect:     dialect,
		showResults: showResults,
		stack:       NewEyrStack(),
	}
	ml := util.NewMicroLiner(c, r.recieveMessage, r.recieveLine)
	r.ml = ml

	ctx, cancel := context.WithCancel(context.Background())

	defer cancel()

	// ctx := context.Background()
	// defer os.Exit(0)
	// defer ctx.Done()
	defer keyboard.Close()
	go func(ctx context.Context) {
		for {
			select {
			case <-ctx.Done():
				// fmt.Println("Done")
				return
			default:
				// fmt.Println("Select default")
				r, k, keyErr := keyboard.GetKey()
				if err != nil {
					fmt.Println(keyErr)
					break
				}
				if k == keyboard.KeyCtrlC {
					// fmt.Println("Ctrl C 1")
					cancel()
					syscall.Kill(os.Getpid(), syscall.SIGINT)
					//ctx.Done()
					// fmt.Println("")
					// return
					//break
					//					os.Exit(0)
				}
				c <- constructKeyEvent(r, k)
			}
		}
	}(ctx)

	// fmt.Println("MICRO")
	_, err = ml.MicroPrompt("x> ", "", 0, ctx)
	if err != nil {
		fmt.Println(err)
	}
	// fmt.Println("END")

}

/*  THIS WAS DISABLED TEMP FOR WASM MODE .. 20250116 func DoGeneralInput(es *env.ProgramState, prompt string) {
	line := liner.NewLiner()
	defer line.Close()
	if code, err := line.SimplePrompt(prompt); err == nil {
		es.Res = *env.NewString(code)
	} else {
		log.Print("Error reading line: ", err)
	}
}

func DoGeneralInputField(es *env.ProgramState, prompt string) {
	line := liner.NewLiner()
	defer line.Close()
	if code, err := line.SimpleTextField(prompt, 5); err == nil {
		es.Res = *env.NewString(code)
	} else {
		log.Print("Error reading line: ", err)
	}
}
*/

func DoRyeRepl_OLD(es *env.ProgramState, showResults bool) { // here because of some odd options we were experimentally adding
	codestr := "a: 100\nb: \"jim\"\nprint 10 + 20 + b"
	codelines := strings.Split(codestr, ",\n")

	line := liner.NewLiner()
	defer line.Close()

	line.SetCtrlCAborts(true)

	line.SetCompleter(func(line string) (c []string) {
		for i := 0; i < es.Idx.GetWordCount(); i++ {
			if strings.HasPrefix(es.Idx.GetWord(i), strings.ToLower(line)) {
				c = append(c, es.Idx.GetWord(i))
			}
		}
		return
	})

	if f, err := os.Open(history_fn); err == nil {
		if _, err := line.ReadHistory(f); err != nil {
			log.Print("Error reading history file: ", err)
		}
		f.Close()
	}
	//const PROMPT = "\x1b[6;30;42m Rye \033[m "

	shellEd := ShellEd{env.Function{}, false, make([]string, 0), "", nil}

	// nek variable bo z listo wordow bo ki jih želi setirat v tem okolju in dokler ne pride čez bo repl spraševal za njih
	// name funkcije pa bo prikazal v promptu dokler smo noter , spet en state var
	// isti potek bi lahko uporabili za kreirat live validation dialekte npr daš primer podatka Dict npr za input in potem pišeš dialekt
	// in preverjaš rezultat ... tako z hitrim reset in ponovi workflowon in prikazom rezultata
	// to s funkcijo se bo dalo čist dobro naredit ... potem pa tudi s kontekstom ne vidim kaj bi bil problem

	line2 := ""

	var prevResult env.Object

	for {
		prompt, arg := genPrompt(&shellEd, line2)

		if code, err := line.Prompt(prompt); err == nil {
			// strip comment

			es.LiveObj.PsMutex.Lock()
			for _, update := range es.LiveObj.Updates {
				fmt.Println("\033[35m((Reloading " + update + "))\033[0m")
				block_, script_ := LoadScriptLocalFile(es, *env.NewUri1(es.Idx, "file://"+update))
				es.Res = EvaluateLoadedValue(es, block_, script_, true)
			}
			es.LiveObj.ClearUpdates()
			es.LiveObj.PsMutex.Unlock()

			multiline := len(code) > 1 && code[len(code)-1:] == " "

			comment := regexp.MustCompile(`\s*;`)
			line1 := comment.Split(code, 2) //--- just very temporary solution for some comments in repl. Later should probably be part of loader ... maybe?
			//fmt.Println(line1)
			lineReal := strings.Trim(line1[0], "\t")

			// fmt.Println("*" + lineReal + "*")

			// JM20201008
			if lineReal == "111" {
				for _, c := range codelines {
					fmt.Println(c)
				}
			}

			// check for #shed commands
			maybeDoShedCommands(lineReal, es, &shellEd)

			///fmt.Println(lineReal[len(lineReal)-3 : len(lineReal)])

			if multiline {
				line2 += lineReal + "\n"
			} else {
				line2 += lineReal

				if strings.Trim(line2, " \t\n\r") == "" {
					// ignore
				} else if strings.Compare("((show-results))", line2) == 0 {
					showResults = true
				} else if strings.Compare("((hide-results))", line2) == 0 {
					showResults = false
				} else if strings.Compare("((return))", line2) == 0 {
					// es.Ser = ser
					// fmt.Println("")
					return
				} else {
					//fmt.Println(lineReal)
					block, genv := loader.LoadString(line2, false)
					block1 := block.(env.Block)
					es = env.AddToProgramState(es, block1.Series, genv)

					// EVAL THE DO DIALECT
					EvalBlockInj(es, prevResult, true)

					if arg != "" {
						if arg == "<-return->" {
							shellEd.Return = es.Res
						} else {
							es.Ctx.Set(es.Idx.IndexWord(arg), es.Res)
						}
					} else {
						if shellEd.Mode != "" {
							if !shellEd.Pause {
								shellEd.CurrObj.Body.Series.AppendMul(block1.Series.GetAll())
							}
						}

						MaybeDisplayFailureOrError(es, genv)

						if !es.ErrorFlag && es.Res != nil {
							prevResult = es.Res
							// TEMP - make conditional
							// print the result
							if showResults {
								fmt.Println("\033[38;5;37m" + es.Res.Inspect(*genv) + "\x1b[0m")
							}
							if es.Res != nil && shellEd.Mode != "" && !shellEd.Pause && es.Res == shellEd.Return {
								fmt.Println(" <- the correct value was returned")
							}
						}

						es.ReturnFlag = false
						es.ErrorFlag = false
						es.FailureFlag = false
					}
				}

				line2 = ""
			}

			line.AppendHistory(code)
		} else if err == liner.ErrPromptAborted {
			// log.Print("Aborted")
			break
			//		} else if err == liner.ErrJMCodeUp {
			/* } else if err == liner.ErrCodeUp { ... REMOVED 04.01.2022 for cleaning , figure out why I added it and if it still makes sense
			fmt.Println("")
			for _, c := range codelines {
				fmt.Println(c)
			}
			MoveCursorUp(len(codelines))*/
		} else {
			log.Print("Error reading line: ", err)
			break
		}
	}

	if f, err := os.Create(history_fn); err != nil {
		log.Print("Error writing history file: ", err)
	} else {
		if _, err := line.WriteHistory(f); err != nil {
			log.Print("Error writing history file: ", err)
		}
		f.Close()
	}
}
