package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"waveboard/fixes/gosamplerate"
	"waveboard/fixes/gosndfile/sndfile"
	"waveboard/fixes/ui"

	"github.com/bep/debounce"
	"github.com/gen2brain/malgo"
	"github.com/lxn/win"
	"github.com/moutend/go-hook/pkg/keyboard"
	"github.com/moutend/go-hook/pkg/types"
	"github.com/nxadm/tail"
)

type Settings struct {
	LogFile          string                 `json:"logfile"`
	LastDirectory    string                 `json:"lastdir"`
	LogWatch         string                 `json:"logwatch"`
	BlockedUsers     []string               `json:"blockedusers"`
	AllowedUsers     []string               `json:"allowedusers"`
	Commands         map[string]*LogCommand `json:"commands"`
	SampleRate       uint32                 `json:"samplerate"`
	Device           string                 `json:"devicename"`
	ResamplerType    int                    `json:"resamplertype"`
	GlobalVolume     float32                `json:"globalvolume"`
	LimiterThreshold float32                `json:"limiterthreshold"`
	AttackTime       float32                `json:"attacktime"`
	ReleaseTime      float32                `json:"releasetime"`
	VideoLimit       int64                  `json:"videosizelimit"`
	QueueLimit       int                    `json:"queuelimit"`
	CommandPrefix    string                 `json:"commandprefix"`
	ChatPrefix       string                 `json:"chatprefix"`
	Timestamped      bool                   `json:"timestamped"`
	TTSVoice         string                 `json:"ttsvoice"`
	TTSVolume        float32                `json:"ttsvolume"`
	TTSRate          float32                `json:"ttsrate"`
	WindowSize       ContentSize            `json:"windowsize"`
	Maximized        bool                   `json:"maximized"`
	Tracks           map[string]*AudioTrack `json:"tracks"`
}

type VirtualShim struct {
	Data  []byte
	Index int64
}

type AudioTrack struct {
	ID          int               `json:"-"`
	Row         int               `json:"-"`
	Extension   string            `json:"-"`
	Name        string            `json:"-"`
	Path        string            `json:"-"`
	Volume      float32           `json:"volume"`
	Binding     types.VKCode      `json:"binding"`
	SampleRatio float64           `json:"-"`
	Data        []float32         `json:"-"`
	ReadMode    bool              `json:"-"`
	Virtual     *sndfile.File     `json:"-"`
	Device      *malgo.Device     `json:"-"`
	Resampler   *gosamplerate.Src `json:"-"`
}

// https://github.com/dylagit/audio-limiter/blob/main/src/compressor.rs

type Compressor struct {
	PeakAtTime float32
	PeakRTime  float32
	PeakAvg    float32
	GainAtTime float32
	GainRTime  float32
	GainAvg    float32
	Threshold  float32
}

type LogCommand struct {
	AllowedOnly bool         `json:"allowedonly"`
	Action      func(string) `json:"-"`
	Description string       `json:"-"`
}

type ContentSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type FilesTableModel struct{}
type CommandsTableModel struct{}
type AllowedTableModel struct{}
type BlockedTableModel struct{}
type QueueTableModel struct{}

const appName = "WaveBoard"
const dateFormat = "02/01/2006 - 15:04:05.0000"
const deleteKey = types.VK_BACK
const defaultVolume float32 = 100.0
const defaultCommandPrefix = `\.`
const defaultTimestampFormat = `\d{2}/\d{2}/\d{4} - \d{2}:\d{2}:\d{2}: `
const defaultChatSeparator = ` :\s{1,2}`
const defaultChatPrefix = `\(TEAM\) |\*DEAD\*\(TEAM\) |\(Spectator\) |\*DEAD\* |\*SPEC\* |\*COACH\* `
const defaultAllowedValue = false
const defaultBlockedValue = true
const defaultTTSVolume = 100.0
const defaultTTSRate = 0.0
const defaultWindowWidth = 960
const defaultWindowHeight = 540
const createNoWindow = 0x08000000

const helpMessage = `name: - searches within the "Name" column
id: - searches within the "ID" column
bind: - searches within the "Binding" column`

var resamplersName []string = func() []string {
	var namesArray []string = nil

	for index := 0; index <= gosamplerate.SRC_LINEAR; index++ {
		resamplerName, nameError := gosamplerate.GetName(index)

		if nameError != nil {
			break
		}

		namesArray = append(namesArray, resamplerName)
	}

	return namesArray
}()

var filtersMap = map[string]func(string){
	"name": searchName,
	"id":   searchID,
	"bind": searchBind,
}

var extensionsMap = map[string]bool{
	".mp3":  true,
	".wav":  true,
	".ogg":  true,
	".flac": true,
}

var commandsList []string = []string{
	"play",
	"fplay",
	"volume",
	"gvolume",
	"samplerate",
	"tts",
	"video",
	"fvideo",
	"skip",
	"skipall",
	"block",
	"allow",
	"removeblock",
	"removeallow",
}

var permissionsMap map[string]bool = map[string]bool{
	"play":        defaultAllowedValue,
	"fplay":       defaultBlockedValue,
	"volume":      defaultBlockedValue,
	"gvolume":     defaultBlockedValue,
	"samplerate":  defaultBlockedValue,
	"tts":         defaultAllowedValue,
	"video":       defaultAllowedValue,
	"fvideo":      defaultBlockedValue,
	"skip":        defaultBlockedValue,
	"skipall":     defaultBlockedValue,
	"block":       defaultBlockedValue,
	"allow":       defaultBlockedValue,
	"removeblock": defaultBlockedValue,
	"removeallow": defaultBlockedValue,
}

var g_appSettings Settings = Settings{
	LogFile:          "",
	LastDirectory:    "",
	LogWatch:         "",
	BlockedUsers:     nil,
	AllowedUsers:     nil,
	Commands:         nil,
	SampleRate:       44100,
	Device:           "",
	ResamplerType:    gosamplerate.SRC_LINEAR,
	GlobalVolume:     defaultVolume,
	LimiterThreshold: 0,
	AttackTime:       25.0,
	ReleaseTime:      50.0,
	VideoLimit:       100,
	QueueLimit:       100,
	CommandPrefix:    defaultCommandPrefix,
	ChatPrefix:       defaultChatPrefix,
	Timestamped:      false,
	TTSVoice:         "",
	TTSVolume:        defaultTTSVolume,
	TTSRate:          defaultTTSRate,
	WindowSize:       ContentSize{defaultWindowWidth, defaultWindowHeight},
	Maximized:        false,
	Tracks:           make(map[string]*AudioTrack),
}
var g_settingsFile *os.File
var g_saveFunc = debounce.New(500 * time.Millisecond)

var g_mainWindow *ui.Window

var g_logFile *os.File
var g_logEntry *ui.MultilineEntry

var g_audioContext *malgo.AllocatedContext
var g_devicesList []malgo.DeviceInfo
var g_initDevices []*malgo.Device
var g_selectedDevice int = -1
var g_defaultDevice int = -1
var g_stopMutex sync.Mutex

var g_keyboardHook chan types.KeyboardEvent
var g_bindingRow int = -1
var g_keysMap map[types.VKCode]*AudioTrack

var g_sampleRateEntry *ui.Entry
var g_globalVolumeEntry *ui.Entry
var g_tracksList []*AudioTrack
var g_audioLimiter *Compressor
var g_currentTrack *AudioTrack
var g_audioBuffer *bytes.Buffer = bytes.NewBuffer(nil)
var g_filteredList []int
var g_filesTableModel *ui.TableModel

var g_watchFile *tail.Tail
var g_chatPrefixRegex *regexp.Regexp
var g_timestampRegex *regexp.Regexp
var g_chatSeparatorRegex *regexp.Regexp
var g_allowedModel *ui.TableModel
var g_blockedModel *ui.TableModel

var g_ttsDevices []string
var g_ttsInPipe io.WriteCloser
var g_voicesList []string
var g_selectedVoice int = -1

var g_audioQueue []*AudioTrack
var g_queueModel *ui.TableModel

var g_logCommands map[string]*LogCommand = map[string]*LogCommand{
	"play":        {permissionsMap["play"], playCommand, "adds a sound to the queue"},
	"fplay":       {permissionsMap["fplay"], forcePlayCommand, "plays a sound"},
	"volume":      {permissionsMap["volume"], setVolumeCommand, "adjusts the volume of the current track"},
	"gvolume":     {permissionsMap["gvolume"], setGlobalVolumeCommand, "adjusts the global volume"},
	"samplerate":  {permissionsMap["samplerate"], setSampleRateCommand, "adjusts the sample rate"},
	"tts":         {permissionsMap["tts"], ttsCommand, "plays TTS"},
	"video":       {permissionsMap["video"], videoCommand, "downloads and adds a video to the queue"},
	"fvideo":      {permissionsMap["fvideo"], forceVideoCommand, "downloads and plays a video"},
	"skip":        {permissionsMap["skip"], skipCommand, "skips the current track"},
	"skipall":     {permissionsMap["skipall"], skipAllCommand, "removes all tracks from the queue"},
	"block":       {permissionsMap["block"], blockCommand, "adds the user to blocked list"},
	"allow":       {permissionsMap["allow"], allowCommand, "adds the user to allowed list"},
	"removeblock": {permissionsMap["removeblock"], removeBlockCommand, "removes the user from the blocked list"},
	"removeallow": {permissionsMap["removeallow"], removeAllowCommand, "removes the user from the allowed list"},
}

var g_sizeChangedFunc = debounce.New(250 * time.Millisecond)

var g_systemWindow win.HWND
var g_windowPlacement *win.WINDOWPLACEMENT = &win.WINDOWPLACEMENT{Length: 6}

func init() {
	g_timestampRegex = regexp.MustCompile(defaultTimestampFormat)

	currentPath, wdError := os.Getwd()

	if wdError != nil {
		return
	}

	var settingsError error
	g_settingsFile, settingsError = os.OpenFile(filepath.Join(currentPath, "waveboard.settings.json"),
		os.O_CREATE|os.O_RDWR, 0644)

	if settingsError != nil {
		return
	}

	settingsContents, readError := io.ReadAll(g_settingsFile)

	if readError != nil {
		g_settingsFile.Close()
		g_settingsFile = nil

		return
	}

	json.Unmarshal(settingsContents, &g_appSettings)

	settingsContents = nil

	if g_appSettings.Commands == nil {
		g_appSettings.Commands = make(map[string]*LogCommand)
	} else {
		for key := range permissionsMap {
			command, exists := g_appSettings.Commands[key]

			if !exists {
				continue
			}

			if command.AllowedOnly == permissionsMap[key] {
				delete(g_appSettings.Commands, key)

				continue
			}

			g_logCommands[key].AllowedOnly = command.AllowedOnly
		}
	}

	if g_appSettings.WindowSize.Width == 0 && g_appSettings.WindowSize.Height == 0 {
		g_appSettings.WindowSize.Width = defaultWindowWidth
		g_appSettings.WindowSize.Height = defaultWindowHeight
	}

	if g_appSettings.ResamplerType > gosamplerate.SRC_LINEAR {
		g_appSettings.ResamplerType = gosamplerate.SRC_LINEAR
	} else if g_appSettings.ResamplerType < gosamplerate.SRC_SINC_BEST_QUALITY {
		g_appSettings.ResamplerType = gosamplerate.SRC_SINC_BEST_QUALITY
	}

	if g_appSettings.Tracks == nil {
		g_appSettings.Tracks = make(map[string]*AudioTrack)

		return
	}

	for key, value := range g_appSettings.Tracks {
		if value.Binding != 0 || value.Volume != defaultVolume {
			continue
		}

		delete(g_appSettings.Tracks, key)
	}
}

func saveSettings() error {
	if g_settingsFile == nil {
		return errors.New("waveboard.settings.json does not exist")
	}

	jsonSettings, marshalError := json.MarshalIndent(&g_appSettings, "", "\t")

	if marshalError != nil {
		return marshalError
	}

	truncateError := g_settingsFile.Truncate(0)

	if truncateError != nil {
		return truncateError
	}

	_, seekError := g_settingsFile.Seek(0, io.SeekStart)

	if seekError != nil {
		return seekError
	}

	jsonSettings = []byte(strings.ReplaceAll(string(jsonSettings), "\n", "\r\n"))
	_, writeError := g_settingsFile.WriteString(string(jsonSettings))

	return writeError
}

func trySaveSettings() {
	g_saveFunc(func() {
		if saveError := saveSettings(); saveError != nil {
			ui.QueueMain(func() { logToEntry(saveError.Error()) })
		}
	})
}

func main() {
	ui.Main(SetupUI)
}

func SetupUI() {
	g_mainWindow = ui.NewWindow(appName, g_appSettings.WindowSize.Width, g_appSettings.WindowSize.Height, false)
	g_systemWindow = findCurrentWindow()
	g_mainWindow.SetMargined(true)

	logTab := makeLogTab()

	logToEntry("Built window and log tab")

	setupRegex()
	setupTTS()
	setupAudio()
	setupKeyboardHook()

	panelTabs := ui.NewTab()
	panelTabs.Append("Log", logTab)
	panelTabs.SetMargined(0, true)

	panelTabs.Append("Audio", makeAudioTab())
	panelTabs.SetMargined(1, true)

	logToEntry("Built audio tab")

	panelTabs.Append("Downloader", makeDownloaderTab())
	panelTabs.SetMargined(2, true)

	logToEntry("Built downloader tab")

	panelTabs.Append("Log watch", makeLogWatchTab())
	panelTabs.SetMargined(3, true)

	logToEntry("Built log watch tab")

	panelTabs.Append("Queue", makeQueueTab())
	panelTabs.SetMargined(4, true)

	logToEntry("Built queue tab")

	panelTabs.Append("TTS", makeTTSTab())
	panelTabs.SetMargined(5, true)

	logToEntry("Built text-to-speech tab")

	panelTabs.Append("Limiter", makeLimiterTab())
	panelTabs.SetMargined(6, true)

	logToEntry("Built limiter tab")

	g_mainWindow.SetChild(panelTabs)

	g_mainWindow.OnClosing(func(w *ui.Window) bool {
		cleanResources()

		ui.Quit()

		return true
	})

	g_mainWindow.OnContentSizeChanged(func(w *ui.Window) {
		g_sizeChangedFunc(func() {
			if g_systemWindow != 0 {
				win.GetWindowPlacement(g_systemWindow, g_windowPlacement)

				if g_windowPlacement.ShowCmd == 3 {
					if !g_appSettings.Maximized {
						g_appSettings.Maximized = true

						go trySaveSettings()
					}

					return
				}

				if g_appSettings.Maximized && g_windowPlacement.ShowCmd == 1 {
					g_appSettings.Maximized = false

					go trySaveSettings()

					return
				}
			}

			newWidth, newHeight := w.GetContentSize()

			if (newWidth == 0 && newHeight == 0) ||
				(newWidth == g_appSettings.WindowSize.Width && newHeight == g_appSettings.WindowSize.Height) {
				return
			}

			g_appSettings.WindowSize.Width = newWidth
			g_appSettings.WindowSize.Height = newHeight

			go trySaveSettings()
		})
	})

	ui.OnShouldQuit(func() bool {
		cleanResources()

		g_mainWindow.Destroy()

		return true
	})

	g_mainWindow.Show()

	if g_appSettings.Maximized && g_systemWindow != 0 {
		win.GetWindowPlacement(g_systemWindow, g_windowPlacement)
		win.SetWindowPlacement(g_systemWindow, &win.WINDOWPLACEMENT{
			Length:           g_windowPlacement.Length,
			Flags:            g_windowPlacement.Flags,
			ShowCmd:          3,
			PtMinPosition:    g_windowPlacement.PtMinPosition,
			PtMaxPosition:    g_windowPlacement.PtMaxPosition,
			RcNormalPosition: g_windowPlacement.RcNormalPosition,
		})
	}
}

func findCurrentWindow() win.HWND {
	windowNamePtr, ptrError := syscall.UTF16PtrFromString(appName)

	if ptrError != nil {
		return 0
	}

	window := win.FindWindow(nil, windowNamePtr)

	windowNamePtr = nil

	return window
}

func makeLogTab() ui.Control {
	vContainer := ui.NewVerticalBox()
	vContainer.SetPadded(true)

	g_logEntry = ui.NewNonWrappingMultilineEntry()
	g_logEntry.SetReadOnly(true)

	vContainer.Append(g_logEntry, true)

	buttonBox := ui.NewHorizontalBox()
	buttonBox.SetPadded(true)

	saveButton := ui.NewButton("Set log file")
	saveButton.OnClicked(func(b *ui.Button) {
		fileSave := ui.OpenFile(g_mainWindow)

		if fileSave == "" {
			return
		}

		fileSave = filepath.ToSlash(fileSave)

		if fileSave == g_appSettings.LogFile {
			logToEntry("Ignored same log output file")

			return
		}

		if fillError := fillLogFile(fileSave); fillError != nil {
			logToEntry(fillError.Error())

			return
		}

		go trySaveSettings()
	})

	if g_appSettings.LogFile != "" {
		if fillError := fillLogFile(g_appSettings.LogFile); fillError != nil {
			logToEntry(fillError.Error())
		}
	}

	buttonBox.Append(saveButton, false)
	vContainer.Append(buttonBox, false)

	return vContainer
}

func logToEntry(text string, params ...any) {
	newText := fmt.Sprintf(time.Now().Format(dateFormat)+": "+text+"\r\n", params...)

	g_logEntry.Append(newText)

	logToFile(newText)
}

func logToFile(text string) {
	if g_logFile == nil {
		return
	}

	_, writeError := g_logFile.WriteString(text)

	if writeError != nil {
		g_logEntry.Append(time.Now().Format(dateFormat) + ": " + writeError.Error() + "\r\n")
	}
}

func fillLogFile(fileSave string) error {
	if fileSave == g_appSettings.LogWatch {
		if g_logFile == nil {
			g_appSettings.LogFile = ""
		}

		return errors.New("FillLogFile : Cannot use log input as output file")
	}

	if g_logFile != nil {
		g_logFile.Close()
		g_logFile = nil
	}

	var fileError error
	g_logFile, fileError = os.OpenFile(filepath.FromSlash(fileSave), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)

	if fileError != nil {
		return fileError
	}

	g_appSettings.LogFile = fileSave

	logToFile(g_logEntry.Text())

	return nil
}

func cleanSettings() {
	g_settingsFile.Close()
	g_settingsFile = nil
}

func cleanAudio() {
	for index := range g_initDevices {
		g_initDevices[index].Uninit()
		g_initDevices[index] = nil
	}

	g_initDevices = nil

	g_audioContext.Uninit()
	g_audioContext.Free()
	g_audioContext = nil
}

func cleanLog() {
	g_logFile.Close()
	g_logFile = nil
}

func cleanKeyboardHook() {
	keyboard.Uninstall()
	close(g_keyboardHook)
	g_keyboardHook = nil
}

func cleanResources() {
	if g_settingsFile != nil {
		cleanSettings()
	}

	if g_audioContext != nil {
		cleanAudio()
	}

	if g_logFile != nil {
		cleanLog()
	}

	if g_keyboardHook != nil {
		cleanKeyboardHook()
	}
}

func setupRegex() {
	var compileError error
	g_chatSeparatorRegex, compileError = regexp.Compile(defaultChatSeparator + g_appSettings.CommandPrefix)

	if compileError != nil {
		logToEntry(compileError.Error())

		g_appSettings.CommandPrefix = defaultCommandPrefix
		g_chatSeparatorRegex = regexp.MustCompile(defaultChatSeparator + g_appSettings.CommandPrefix)

		go trySaveSettings()
	}

	g_chatPrefixRegex, compileError = regexp.Compile(g_appSettings.ChatPrefix)

	if compileError != nil {
		logToEntry(compileError.Error())

		g_appSettings.ChatPrefix = defaultChatPrefix
		g_chatPrefixRegex = regexp.MustCompile(g_appSettings.ChatPrefix)

		go trySaveSettings()
	}
}

func setupTTS() {
	ttsPath, pathError := exec.LookPath("./tts.vbs")

	if pathError != nil {
		logToEntry(pathError.Error())

		return
	}

	var cscriptPath = "cscript"

	if _, statError := os.Stat(filepath.Join(os.Getenv("SYSTEMROOT"), "SysWow64")); statError == nil {
		cscriptPath = filepath.Join(os.Getenv("SYSTEMROOT"), "SysWow64", "cscript")
	}

	ttsBin := exec.Command(cscriptPath, "//nologo", "//b", ttsPath)
	ttsBin.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}

	var pipeError error
	g_ttsInPipe, pipeError = ttsBin.StdinPipe()

	if pipeError != nil {
		ttsBin = nil

		logToEntry(pipeError.Error())

		return
	}

	var ttsOutPipe io.ReadCloser
	ttsOutPipe, pipeError = ttsBin.StdoutPipe()

	if pipeError != nil {
		ttsBin = nil

		if g_ttsInPipe != nil {
			g_ttsInPipe.Close()
			g_ttsInPipe = nil
		}

		logToEntry(pipeError.Error())

		return
	}

	startError := ttsBin.Start()

	if startError != nil {
		ttsBin = nil

		if ttsOutPipe != nil {
			ttsOutPipe.Close()
			ttsOutPipe = nil
		}

		if g_ttsInPipe != nil {
			g_ttsInPipe.Close()
			g_ttsInPipe = nil
		}

		logToEntry(startError.Error())

		return
	}

	g_ttsInPipe.Write([]byte("ListVoices\r\n"))

	linesScanner := bufio.NewScanner(ttsOutPipe)

	for linesScanner.Scan() {
		if linesScanner.Text() == "End of voices list" {
			break
		}

		g_voicesList = append(g_voicesList, linesScanner.Text())
	}

	g_ttsInPipe.Write([]byte("ListDevices\r\n"))

	for linesScanner.Scan() {
		if linesScanner.Text() == "End of devices list" {
			if ttsOutPipe != nil {
				ttsOutPipe.Close()
				ttsBin.Stdout = nil
				ttsOutPipe = nil
			}

			break
		}

		g_ttsDevices = append(g_ttsDevices, linesScanner.Text())
	}

	linesScanner = nil

	if g_appSettings.TTSVolume > 100 || g_appSettings.TTSVolume < 0 {
		g_appSettings.TTSVolume = defaultTTSVolume

		go trySaveSettings()
	}

	setTTSVolume(g_appSettings.TTSVolume)

	if g_appSettings.TTSRate > 10 || g_appSettings.TTSVolume < -10 {
		g_appSettings.TTSRate = defaultTTSRate

		go trySaveSettings()
	}

	setTTSRate(g_appSettings.TTSRate)

	if g_appSettings.TTSVoice == "" {
		return
	}

	for index := range g_voicesList {
		if g_voicesList[index] != g_appSettings.TTSVoice {
			continue
		}

		g_selectedVoice = index

		break
	}

	if g_selectedVoice == -1 {
		logToEntry("Saved TTS voice could not be found")
	}
}

func initializeAudioContext() error {
	var audioError error = nil
	g_audioContext, audioError = malgo.InitContext(nil, malgo.ContextConfig{}, nil)

	return audioError
}

func retrieveDevicesList() error {
	var audioError error = nil
	g_devicesList, audioError = g_audioContext.Devices(malgo.Playback)

	for currentIndex := 0; currentIndex < len(g_ttsDevices); currentIndex++ {
		listDevice := g_devicesList[currentIndex]

		if listDevice.Name() == g_ttsDevices[currentIndex] {
			continue
		}

		for swapIndex := range g_ttsDevices {
			if g_ttsDevices[swapIndex] != listDevice.Name() {
				continue
			}

			g_devicesList[currentIndex] = g_devicesList[swapIndex]
			g_devicesList[swapIndex] = listDevice

			currentIndex = 0

			break
		}
	}

	return audioError
}

func initializeDevices() error {
	for _, item := range g_devicesList {
		deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
		deviceConfig.Playback.Format = malgo.FormatF32
		deviceConfig.Playback.Channels = 2
		deviceConfig.SampleRate = g_appSettings.SampleRate
		deviceConfig.Playback.DeviceID = item.ID.Pointer()

		deviceCallbacks := malgo.DeviceCallbacks{
			Data: DataFunc,
		}

		initDevice, deviceError := malgo.InitDevice(g_audioContext.Context, deviceConfig, deviceCallbacks)

		if deviceError != nil {
			return deviceError
		}

		g_initDevices = append(g_initDevices, initDevice)
		initDevice = nil
	}

	return nil
}

func setupAudio() {
	audioError := initializeAudioContext()

	if audioError != nil {
		logToEntry(audioError.Error())

		return
	}

	logToEntry("Initialized audio context")

	audioError = retrieveDevicesList()

	if audioError != nil {
		logToEntry(audioError.Error())

		g_audioContext.Uninit()
		g_audioContext.Free()

		return
	}

	logToEntry("Retrieved audio devices list")

	audioError = initializeDevices()

	if audioError != nil {
		logToEntry(audioError.Error())
	}

	logToEntry("Initialized %d audio devices", len(g_initDevices))

	for index := range g_initDevices {
		if g_devicesList[index].IsDefault != 1 {
			continue
		}

		g_defaultDevice = index

		break
	}

	if g_appSettings.Device == "" {
		return
	}

	for index := range g_initDevices {
		if g_devicesList[index].Name() != g_appSettings.Device {
			continue
		}

		g_selectedDevice = index

		break
	}

	if g_selectedDevice == -1 {
		logToEntry("Saved device not found or is not initialized")
	}
}

func setupKeyboardHook() {
	g_keysMap = make(map[types.VKCode]*AudioTrack)
	g_keyboardHook = make(chan types.KeyboardEvent)

	if kbError := keyboard.Install(nil, g_keyboardHook); kbError != nil {
		logToEntry(kbError.Error())

		return
	}

	logToEntry("Initialized keyboard hook")

	go kbHookCallback()
}

func kbHookCallback() {
	for elem := range g_keyboardHook {
		if elem.Message != types.WM_KEYDOWN {
			continue
		}

		if g_bindingRow == -1 {
			if elem.VKCode == deleteKey {
				continue
			}

			track, exists := g_keysMap[elem.VKCode]

			if !exists {
				continue
			}

			go tryPlaySound(track, g_selectedDevice)

			continue
		}

		if elem.VKCode == deleteKey {
			rowTrack := g_tracksList[getFilteredID(g_bindingRow)]

			if rowTrack.Binding == 0 {
				g_filesTableModel.RowChanged(g_bindingRow)
				g_bindingRow = -1

				continue
			}

			boundKey := rowTrack.Binding

			rowTrack.Binding = 0
			delete(g_keysMap, boundKey)
			g_filesTableModel.RowChanged(g_bindingRow)
			g_appSettings.Tracks[filepath.ToSlash(rowTrack.Path)] = rowTrack

			if rowTrack.Volume == defaultVolume {
				delete(g_appSettings.Tracks, filepath.ToSlash(rowTrack.Path))
			}

			g_bindingRow = -1
			rowTrack = nil

			go trySaveSettings()

			continue
		}

		rowTrack := g_tracksList[getFilteredID(g_bindingRow)]

		if track, exists := g_keysMap[elem.VKCode]; exists {
			track.Binding = 0

			if track.Volume == defaultVolume {
				delete(g_appSettings.Tracks, filepath.ToSlash(track.Path))
			}

			g_filesTableModel.RowChanged(track.GetRow())
		}

		if trackBind := rowTrack.Binding; trackBind != 0 {
			boundRow := getFilteredID(g_bindingRow)
			delete(g_keysMap, g_tracksList[boundRow].Binding)

			if g_tracksList[boundRow].Volume == defaultVolume {
				delete(g_appSettings.Tracks, filepath.ToSlash(g_tracksList[boundRow].Path))
			}
		}

		rowTrack.Binding = elem.VKCode
		g_keysMap[elem.VKCode] = rowTrack
		g_filesTableModel.RowChanged(g_bindingRow)
		g_appSettings.Tracks[filepath.ToSlash(rowTrack.Path)] = rowTrack
		g_bindingRow = -1

		rowTrack = nil

		go trySaveSettings()
	}
}

func makeAudioTab() ui.Control {
	vContainer := ui.NewVerticalBox()
	vContainer.SetPadded(true)

	audioForm := ui.NewForm()
	audioForm.SetPadded(true)

	devicesComboBox := ui.NewCombobox()

	for _, item := range g_devicesList {
		devicesComboBox.Append(item.Name())
	}

	devicesComboBox.SetSelected(g_selectedDevice)
	devicesComboBox.OnSelected(func(c *ui.Combobox) {
		selectedItem := c.Selected()

		if selectedItem == g_selectedDevice {
			return
		}

		g_stopMutex.Lock()
		defer g_stopMutex.Unlock()

		g_selectedDevice = selectedItem

		if g_currentTrack != nil && g_currentTrack.Device != nil && g_initDevices[g_selectedDevice] != g_currentTrack.Device {
			g_currentTrack.Device.Stop()

			g_currentTrack.Device = g_initDevices[g_selectedDevice]
			g_currentTrack.StartDevice(g_selectedDevice)
		}

		g_appSettings.Device = g_devicesList[g_selectedDevice].Name()

		go trySaveSettings()
	})

	audioForm.Append("Audio output device :", devicesComboBox, false)

	sampleRateGrid := ui.NewGrid()
	sampleRateGrid.SetPadded(true)

	g_sampleRateEntry = ui.NewEntry()
	g_sampleRateEntry.SetText(strconv.FormatUint(uint64(g_appSettings.SampleRate), 10))

	sampleRateGrid.Append(g_sampleRateEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	sampleRateButton := ui.NewButton("Apply new sample rate")
	sampleRateButton.OnClicked(func(b *ui.Button) {
		newSampleRate, convError := strconv.ParseUint(g_sampleRateEntry.Text(), 10, 32)

		if convError != nil {
			logToEntry(convError.Error())

			return
		}

		if newSampleRate == uint64(g_appSettings.SampleRate) {
			return
		}

		updateSampleRate(uint32(newSampleRate))

		go trySaveSettings()
	})

	sampleRateGrid.Append(sampleRateButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	audioForm.Append("Sample rate :", sampleRateGrid, false)

	globalVolumeGrid := ui.NewGrid()
	globalVolumeGrid.SetPadded(true)

	g_globalVolumeEntry = ui.NewEntry()
	g_globalVolumeEntry.SetText(strconv.FormatFloat(float64(g_appSettings.GlobalVolume), 'f', 2, 32))

	globalVolumeGrid.Append(g_globalVolumeEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	globalVolumeButton := ui.NewButton("Apply new volume")
	globalVolumeButton.OnClicked(func(b *ui.Button) {
		newGlobalVolume, convError := strconv.ParseFloat(g_globalVolumeEntry.Text(), 32)

		if convError != nil {
			logToEntry(convError.Error())

			return
		}

		if newGlobalVolume == float64(g_appSettings.GlobalVolume) {
			return
		}

		g_appSettings.GlobalVolume = float32(newGlobalVolume)

		go trySaveSettings()
	})

	globalVolumeGrid.Append(globalVolumeButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	audioForm.Append("Global volume (%) :", globalVolumeGrid, false)

	resamplerComboBox := ui.NewCombobox()

	for _, item := range resamplersName {
		resamplerComboBox.Append(item)
	}

	resamplerComboBox.SetSelected(g_appSettings.ResamplerType)
	resamplerComboBox.OnSelected(func(c *ui.Combobox) {
		selectedItem := c.Selected()

		if selectedItem == g_appSettings.ResamplerType {
			return
		}

		g_stopMutex.Lock()
		defer g_stopMutex.Unlock()

		g_appSettings.ResamplerType = selectedItem

		if g_currentTrack != nil && g_currentTrack.Device != nil {
			g_currentTrack.Device.Stop()
		}

		for _, item := range g_tracksList {
			if item.Resampler != nil {
				item.ClearResampler()
			}
		}

		if g_currentTrack != nil && g_currentTrack.Device != nil {
			g_currentTrack.MakeResampler()
			g_currentTrack.StartDevice(-1)
		}

		go trySaveSettings()
	})

	audioForm.Append("Resampler :", resamplerComboBox, false)

	directoryGrid := ui.NewGrid()
	directoryGrid.SetPadded(true)

	folderEntry := ui.NewEntry()
	folderEntry.SetReadOnly(true)
	folderEntry.SetText(g_appSettings.LastDirectory)

	directoryGrid.Append(folderEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	folderButton := ui.NewButton("Open directory")
	folderButton.OnClicked(func(b *ui.Button) {
		audioFolder := ui.OpenFolder(g_mainWindow)

		if audioFolder == "" {
			return
		}

		directoryPath := filepath.ToSlash(audioFolder)

		if directoryPath == g_appSettings.LastDirectory {
			logToEntry("Ignored same audio directory")

			return
		}

		g_appSettings.LastDirectory = directoryPath

		if dirError := fillTracksList(); dirError != nil {
			logToEntry(dirError.Error())

			return
		}

		folderEntry.SetText(g_appSettings.LastDirectory)
		g_filesTableModel.RowInserted(0)

		go trySaveSettings()
	})

	directoryGrid.Append(folderButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	audioForm.Append("Audio folder :", directoryGrid, false)

	searchForm := ui.NewForm()
	searchForm.SetPadded(true)

	searchGrid := ui.NewGrid()
	searchGrid.SetPadded(true)

	searchEntry := ui.NewSearchEntry()
	searchEntry.OnChanged(func(e *ui.Entry) {
		if g_tracksList == nil {
			return
		}

		defer g_filesTableModel.RowInserted(0)

		g_filteredList = nil
		fullQuery := e.Text()

		if fullQuery == "" {
			return
		}

		queryPrefix, noPrefixQuery, _ := strings.Cut(fullQuery, ":")

		if noPrefixQuery == "" {
			return
		}

		searchFunc, exists := filtersMap[queryPrefix]

		if !exists {
			return
		}

		searchFunc(strings.TrimSpace(strings.ToUpper(noPrefixQuery)))
	})

	searchGrid.Append(searchEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	searchHelp := ui.NewButton("Help")
	searchHelp.OnClicked(func(b *ui.Button) {
		ui.MsgBox(g_mainWindow, "Search prefixes", helpMessage)
	})

	searchGrid.Append(searchHelp, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	searchForm.Append("Search :", searchGrid, false)

	filesGroup := ui.NewGroup("Files")
	filesGroup.SetMargined(true)

	g_filesTableModel = ui.NewTableModel(&FilesTableModel{})
	filesTable := ui.NewTable(&ui.TableParams{
		Model:                         g_filesTableModel,
		RowBackgroundColorModelColumn: 6,
	})

	filesTable.AppendTextColumn("ID", 0, ui.TableModelColumnNeverEditable, &ui.TableTextColumnOptionalParams{ColorModelColumn: -1})
	filesTable.AppendTextColumn("Name", 1, ui.TableModelColumnNeverEditable, &ui.TableTextColumnOptionalParams{ColorModelColumn: -1})
	filesTable.AppendTextColumn("Extension", 2, ui.TableModelColumnNeverEditable, &ui.TableTextColumnOptionalParams{ColorModelColumn: -1})
	filesTable.AppendTextColumn("Volume (%)", 3, ui.TableModelColumnAlwaysEditable, &ui.TableTextColumnOptionalParams{ColorModelColumn: -1})
	filesTable.AppendButtonColumn("Binding", 4, ui.TableModelColumnAlwaysEditable)
	filesTable.AppendButtonColumn("Preview", 5, ui.TableModelColumnAlwaysEditable)

	filesGroup.SetChild(filesTable)

	if g_appSettings.LastDirectory != "" {
		if dirError := fillTracksList(); dirError != nil {
			logToEntry(dirError.Error())
		} else {
			g_filesTableModel.RowInserted(0)
		}
	}

	vContainer.Append(audioForm, false)
	vContainer.Append(ui.NewHorizontalSeparator(), false)
	vContainer.Append(ui.NewLabel("Use backspace to unbind tracks."), false)
	vContainer.Append(searchForm, false)
	vContainer.Append(filesGroup, true)

	return vContainer
}

func updateSampleRate(newSampleRate uint32) {
	g_stopMutex.Lock()
	defer g_stopMutex.Unlock()

	g_appSettings.SampleRate = newSampleRate

	var deviceIndex int = -1

	if g_currentTrack != nil && g_currentTrack.Device != nil {
		for index := range g_initDevices {
			if g_initDevices[index] != g_currentTrack.Device {
				continue
			}

			deviceIndex = index

			break
		}

		g_currentTrack.ClearDevice()
		g_audioBuffer.Reset()
		g_currentTrack.ReadMode = false
	}

	for _, item := range g_tracksList {
		item.Data = nil
		item.SampleRatio = -1

		if item.Resampler == nil {
			continue
		}

		item.ClearResampler()
	}

	for index := range g_initDevices {
		g_initDevices[index].Uninit()
		g_initDevices[index] = nil
	}

	g_initDevices = nil

	if initError := initializeDevices(); initError != nil {
		logToEntry(initError.Error())

		return
	}

	if g_currentTrack != nil {
		g_currentTrack.CalculateSampleRatio()
		g_currentTrack.MakeData()
		g_currentTrack.MakeResampler()

		if deviceIndex != -1 {
			g_currentTrack.Device = g_initDevices[deviceIndex]
			g_currentTrack.StartDevice(deviceIndex)
		}
	}
}

func fillTracksList() error {
	for index := range g_tracksList {
		if g_tracksList[index] == g_currentTrack {
			continue
		}

		g_tracksList[index].ClearTrackSafe()
		g_tracksList[index] = nil
	}

	g_tracksList = nil
	g_filteredList = nil

	if g_currentTrack != nil {
		g_currentTrack.ID = -1
	}

	for index := range g_audioQueue {
		g_audioQueue[index].ID = -1
	}

	var trackID int
	folderError := filepath.WalkDir(filepath.FromSlash(g_appSettings.LastDirectory), func(fullFilePath string, dirEntry fs.DirEntry, walkError error) error {
		var fileExt string = filepath.Ext(fullFilePath)

		if walkError != nil {
			return walkError
		}

		if dirEntry.IsDir() || !(extensionsMap[fileExt]) {
			return nil
		}

		track := &AudioTrack{
			ID:          trackID,
			Row:         trackID,
			Extension:   fileExt,
			Name:        strings.TrimSuffix(dirEntry.Name(), fileExt),
			Path:        fullFilePath,
			Volume:      defaultVolume,
			Binding:     0,
			SampleRatio: -1,
			Data:        nil,
			ReadMode:    false,
			Virtual:     nil,
			Device:      nil,
			Resampler:   nil,
		}

		if g_appSettings.Tracks != nil {
			savedTrack, exists := g_appSettings.Tracks[filepath.ToSlash(fullFilePath)]

			if exists {
				track.Volume = savedTrack.Volume
				track.Binding = savedTrack.Binding
				g_keysMap[track.Binding] = track
			}
		}

		g_tracksList = append(g_tracksList, track)

		track = nil

		trackID++

		return nil
	})

	return folderError
}

func searchName(name string) {
	for _, item := range g_tracksList {
		if !strings.Contains(strings.ToUpper(item.Name), name) {
			continue
		}

		item.Row = len(g_filteredList)
		g_filteredList = append(g_filteredList, item.ID)
	}
}

func searchID(id string) {
	index, parseError := strconv.ParseUint(id, 10, 32)

	if (parseError != nil) || (int(index) >= len(g_tracksList)) {
		return
	}

	g_tracksList[index].Row = 0
	g_filteredList = append(g_filteredList, g_tracksList[index].ID)
}

func searchBind(bind string) {
	for _, item := range g_tracksList {
		if !strings.Contains(strings.ReplaceAll(item.Binding.String(), "VK_", ""), bind) {
			continue
		}

		item.Row = len(g_filteredList)
		g_filteredList = append(g_filteredList, item.ID)
	}
}

func getFilteredID(index int) int {
	if g_filteredList != nil {
		return g_filteredList[index]
	}

	return index
}

func (mh *FilesTableModel) ColumnTypes(m *ui.TableModel) []ui.TableValue {
	return []ui.TableValue{
		ui.TableString(""),
		ui.TableString(""),
		ui.TableString(""),
		ui.TableString(""),
		ui.TableString(""),
		ui.TableString(""),
		ui.TableColor{},
	}
}

func (mh *FilesTableModel) NumRows(m *ui.TableModel) int {
	if len(g_tracksList) == 0 {
		return 0
	}

	if g_filteredList != nil {
		return len(g_filteredList) - 1
	}

	return len(g_tracksList) - 1
}

func (mh *FilesTableModel) CellValue(m *ui.TableModel, row, column int) ui.TableValue {
	switch column {
	case 0:
		if g_tracksList == nil {
			return ui.TableString("")
		}

		return ui.TableString(strconv.FormatInt(int64(g_tracksList[getFilteredID(row)].ID), 10))
	case 1:
		if g_tracksList == nil {
			return ui.TableString("")
		}

		return ui.TableString(g_tracksList[getFilteredID(row)].Name)
	case 2:
		if g_tracksList == nil {
			return ui.TableString("")
		}

		return ui.TableString(g_tracksList[getFilteredID(row)].Extension)
	case 3:
		if g_tracksList == nil {
			return ui.TableString("")
		}

		return ui.TableString(strconv.FormatFloat(float64(g_tracksList[getFilteredID(row)].Volume), 'f', 2, 32))
	case 4:
		if g_tracksList == nil {
			return ui.TableString("")
		}

		if g_bindingRow == row {
			return ui.TableString("Binding to ...")
		}

		if g_tracksList[getFilteredID(row)].Binding != 0 {
			return ui.TableString(strings.ReplaceAll(g_tracksList[getFilteredID(row)].Binding.String(), "VK_", ""))
		}

		return ui.TableString("Bind to key")
	case 5:
		if g_tracksList == nil {
			return ui.TableString("")
		}

		return ui.TableString("Preview")
	case 6:
		if row%2 == 0 {
			return ui.TableColor{R: 0, G: 0, B: 0, A: 0.05}
		}

		return nil
	}

	return nil
}

func (mh *FilesTableModel) SetCellValue(m *ui.TableModel, row, column int, value ui.TableValue) {
	if value == nil {
		switch column {
		case 4:
			if g_tracksList == nil {
				return
			}

			if g_bindingRow != -1 {
				m.RowChanged(g_bindingRow)
			}

			g_bindingRow = row
		case 5:
			if g_tracksList == nil {
				return
			}

			go tryPlaySound(g_tracksList[getFilteredID(row)], -1)
		}

		return
	}

	switch column {
	case 3:
		if g_tracksList == nil {
			return
		}

		newVolume, parseError := strconv.ParseFloat(string(value.(ui.TableString)), 32)

		if parseError != nil {
			logToEntry(parseError.Error())

			return
		}

		rowTrack := g_tracksList[getFilteredID(row)]

		if rowTrack.Volume == float32(newVolume) {
			return
		}

		rowTrack.Volume = float32(newVolume)

		g_appSettings.Tracks[filepath.ToSlash(rowTrack.Path)] = rowTrack

		if (newVolume == float64(defaultVolume)) && (rowTrack.Binding == 0) {
			delete(g_appSettings.Tracks, filepath.ToSlash(rowTrack.Path))
		}

		rowTrack = nil

		go trySaveSettings()
	}
}

func playSound(track *AudioTrack, deviceID int) error {
	if g_initDevices == nil {
		return errors.New("PlaySound : No initialized audio devices found")
	}

	g_stopMutex.Lock()
	defer g_stopMutex.Unlock()

	if g_currentTrack != nil && g_currentTrack.Device != nil {
		g_currentTrack.Device.Stop()
		g_currentTrack.Device = nil

		if g_currentTrack.ID == -1 {
			g_currentTrack.ClearTrackSafe()
		}

		g_currentTrack = nil
	}

	if track.Virtual == nil {
		if virtualError := track.MakeVirtual(); virtualError != nil {
			return virtualError
		}
	}

	if _, seekError := track.Virtual.Seek(0, sndfile.Set); seekError != nil {
		return seekError
	}

	if track.SampleRatio == -1 {
		track.CalculateSampleRatio()
	}

	if track.Data == nil {
		track.MakeData()
	}

	if track.Resampler == nil {
		if resamplerError := track.MakeResampler(); resamplerError != nil {
			return resamplerError
		}
	}

	if g_audioLimiter == nil {
		g_audioLimiter = &Compressor{
			PeakAtTime: CalcTau(g_appSettings.SampleRate, 0.01),
			PeakRTime:  CalcTau(g_appSettings.SampleRate, 10.0),
			PeakAvg:    0,
			GainAtTime: CalcTau(g_appSettings.SampleRate, g_appSettings.AttackTime),
			GainRTime:  CalcTau(g_appSettings.SampleRate, g_appSettings.ReleaseTime),
			GainAvg:    1.0,
			Threshold:  g_appSettings.LimiterThreshold,
		}
	} else {
		g_audioLimiter.PeakAtTime = CalcTau(g_appSettings.SampleRate, 0.01)
		g_audioLimiter.PeakRTime = CalcTau(g_appSettings.SampleRate, 10.0)
		g_audioLimiter.PeakAvg = 0
		g_audioLimiter.GainAtTime = CalcTau(g_appSettings.SampleRate, g_appSettings.AttackTime)
		g_audioLimiter.GainRTime = CalcTau(g_appSettings.SampleRate, g_appSettings.ReleaseTime)
		g_audioLimiter.GainAvg = 1.0
		g_audioLimiter.Threshold = g_appSettings.LimiterThreshold
	}

	track.Resampler.Reset()
	g_audioBuffer.Reset()
	track.ReadMode = false
	g_currentTrack = track

	if deviceID != -1 {
		g_currentTrack.Device = g_initDevices[deviceID]

		return g_currentTrack.StartDevice(deviceID)
	}

	g_currentTrack.Device = g_initDevices[g_defaultDevice]

	return g_currentTrack.StartDevice(g_defaultDevice)
}

func tryPlaySound(track *AudioTrack, deviceID int) {
	if playError := playSound(track, deviceID); playError != nil {
		ui.QueueMain(func() { logToEntry(playError.Error()) })
	}
}

func DataFunc(pOutputSample, pInputSamples []byte, framecount uint32) {
	if g_currentTrack == nil || g_currentTrack.Device == nil {
		return
	}

	var numFrames int64
	var frameError error

	if !g_currentTrack.ReadMode {
		numFrames, frameError = g_currentTrack.Virtual.ReadFrames(g_currentTrack.Data)
		numFrames *= int64(g_currentTrack.Virtual.Format.Channels)

		if numFrames == 0 || frameError != nil {
			g_currentTrack.ReadMode = true
		} else {
			finalData, resampleError := g_currentTrack.Resampler.Process(
				g_currentTrack.Data[:numFrames], g_currentTrack.SampleRatio, false)

			if resampleError != nil {
				return
			}

			var bits uint32
			var result float32

			for _, item := range finalData {
				result = g_audioLimiter.Compress(item * g_currentTrack.Volume * g_appSettings.GlobalVolume / 10000)

				if result < -1 {
					result = -1
				} else if result > 1 {
					result = 1
				}

				bits = math.Float32bits(result)

				g_audioBuffer.Write([]byte{
					byte(bits),
					byte(bits >> 8),
					byte(bits >> 16),
					byte(bits >> 24),
				})

				if g_currentTrack.Virtual.Format.Channels != 1 {
					continue
				}

				g_audioBuffer.Write([]byte{
					byte(bits),
					byte(bits >> 8),
					byte(bits >> 16),
					byte(bits >> 24),
				})
			}

			finalData = nil
		}
	}

	numRead, readError := g_audioBuffer.Read(pOutputSample)

	if g_audioBuffer.Len() <= len(pOutputSample) {
		g_currentTrack.ReadMode = false
	} else {
		g_currentTrack.ReadMode = true
	}

	if !(numRead == 0 || readError != nil) || !(numFrames == 0 || frameError != nil) {
		return
	}

	go g_currentTrack.Device.Stop()
	g_currentTrack.Device = nil

	if g_currentTrack.ID == -1 {
		g_currentTrack.ClearTrackSafe()
	}

	g_currentTrack = nil

	if len(g_audioQueue) == 0 {
		return
	}

	go tryPlaySound(g_audioQueue[0], g_selectedDevice)

	g_audioQueue[0] = nil
	g_audioQueue = g_audioQueue[1:]

	ui.QueueMain(func() { g_queueModel.RowInserted(0) })
}

func VirtualShimGetLength(userdata interface{}) int64 {
	return int64(len(userdata.(*VirtualShim).Data))
}

func VirtualShimSeek(offset int64, whence sndfile.Whence, userdata interface{}) int64 {
	shim := userdata.(*VirtualShim)

	var result int64

	switch whence {
	case sndfile.Set:
		result = offset
	case sndfile.Current:
		result = shim.Index + offset
	case sndfile.End:
		result = int64(len(shim.Data)) + offset
	default:
		return 0
	}

	shim.Index = int64(result)

	return shim.Index
}

func VirtualShimRead(output []byte, userdata interface{}) int64 {
	shim := userdata.(*VirtualShim)

	readNum := copy(output, shim.Data[shim.Index:])
	shim.Index += int64(readNum)

	return int64(readNum)
}

func VirtualShimWrite(input []byte, userdata interface{}) int64 {
	shim := userdata.(*VirtualShim)

	writeNum := copy(shim.Data[shim.Index:], input)
	shim.Index += int64(writeNum)

	return int64(writeNum)
}

func VirtualShimTell(userdata interface{}) int64 {
	return userdata.(*VirtualShim).Index
}

func fixAudioFile(audioFix *sndfile.File) (*sndfile.File, error) {
	defer audioFix.Close()

	audioData := make([]float32, audioFix.Format.Channels*int32(audioFix.Format.Frames))

	numFrames, frameError := audioFix.ReadFrames(audioData)

	if numFrames == 0 {
		audioData = nil

		return nil, errors.New("FixAudioFile: no frames read")
	}

	if frameError != nil {
		audioData = nil

		return nil, frameError
	}

	var bytesBuffer []byte = make([]byte, len(audioData))

	writeInfo := sndfile.Info{
		Samplerate: audioFix.Format.Samplerate,
		Channels:   audioFix.Format.Channels,
		Format:     audioFix.Format.Format,
	}

	writeIo := sndfile.VirtualIo{
		UserData:  &VirtualShim{bytesBuffer, 0},
		GetLength: VirtualShimGetLength,
		Seek:      VirtualShimSeek,
		Read:      VirtualShimRead,
		Write:     VirtualShimWrite,
		Tell:      VirtualShimTell,
	}

	writeFile, virtualError := sndfile.OpenVirtual(writeIo, sndfile.Write, &writeInfo)

	if virtualError != nil {
		writeFile.Close()
		writeFile = nil
		writeIo.UserData.(*VirtualShim).Data = nil
		writeIo.UserData = nil
		audioData = nil
		bytesBuffer = nil

		return nil, virtualError
	}

	if _, writeError := writeFile.WriteFrames(audioData); writeError != nil {
		writeFile.Close()
		writeFile = nil
		writeIo.UserData.(*VirtualShim).Data = nil
		writeIo.UserData = nil
		audioData = nil
		bytesBuffer = nil

		return nil, writeError
	}

	writeFile.Close()
	writeFile = nil
	writeIo.UserData.(*VirtualShim).Data = nil
	writeIo.UserData = nil
	audioData = nil

	readIo := sndfile.VirtualIo{
		UserData:  &VirtualShim{bytesBuffer, 0},
		GetLength: VirtualShimGetLength,
		Seek:      VirtualShimSeek,
		Read:      VirtualShimRead,
		Write:     VirtualShimWrite,
		Tell:      VirtualShimTell,
	}

	readFile, readError := sndfile.OpenVirtual(readIo, sndfile.Read, new(sndfile.Info))

	if readError != nil {
		readFile.Close()
		readFile = nil
		readIo.UserData.(*VirtualShim).Data = nil
		readIo.UserData = nil
		bytesBuffer = nil

		return nil, readError
	}

	bytesBuffer = nil

	return readFile, nil
}

func (track *AudioTrack) ClearVirtual() {
	track.Virtual.Close()
	track.Virtual = nil
}

func (track *AudioTrack) ClearResampler() {
	gosamplerate.Delete(*track.Resampler)
	track.Resampler = nil
}

func (track *AudioTrack) ClearBinding() {
	g_keysMap[track.Binding] = nil
	delete(g_keysMap, track.Binding)
	track.Binding = 0
}

func (track *AudioTrack) ClearDevice() {
	track.Device.Stop()
	track.Device = nil
}

func (track *AudioTrack) MakeVirtual() error {
	audioCache, fileError := os.ReadFile(track.Path)

	if fileError != nil {
		return fileError
	}

	virtualIo := sndfile.VirtualIo{
		UserData:  &VirtualShim{audioCache, 0},
		GetLength: VirtualShimGetLength,
		Seek:      VirtualShimSeek,
		Read:      VirtualShimRead,
		Write:     VirtualShimWrite,
		Tell:      VirtualShimTell,
	}

	audioCache = nil
	audioFile, virtualError := sndfile.OpenVirtual(virtualIo, sndfile.Read, new(sndfile.Info))

	if virtualError != nil {
		audioFile.Close()
		audioFile = nil
		virtualIo.UserData.(*VirtualShim).Data = nil
		virtualIo.UserData = nil

		ui.QueueMain(func() { logToEntry("Opening %s%s using disk", track.Name, track.Extension) })

		audioFix, openError := sndfile.Open(track.Path, sndfile.Read, new(sndfile.Info))

		if openError != nil {
			audioFix.Close()
			audioFix = nil

			return openError
		}

		var fixError error
		audioFile, fixError = fixAudioFile(audioFix)
		audioFix = nil

		if fixError != nil {
			return fixError
		}
	}

	track.Virtual = audioFile

	return nil
}

func (track *AudioTrack) CalculateSampleRatio() {
	track.SampleRatio = float64(g_appSettings.SampleRate) / float64(track.Virtual.Format.Samplerate)
}

func (track *AudioTrack) MakeData() {
	dataSize := float64(track.Virtual.Format.Samplerate) / 100

	if (track.SampleRatio < 1) && (track.SampleRatio > 0) {
		dataSize /= track.SampleRatio
	} else {
		dataSize *= track.SampleRatio
	}

	track.Data = make([]float32, int(float64(track.Virtual.Format.Channels)*dataSize))
}

func (track *AudioTrack) MakeResampler() error {
	resamplerSize := float64(track.Virtual.Format.Samplerate) / 100

	if (track.SampleRatio < 1) && (track.SampleRatio > 0) {
		resamplerSize /= track.SampleRatio * track.SampleRatio
	} else {
		resamplerSize *= track.SampleRatio * track.SampleRatio
	}

	resampler, resamplerError := gosamplerate.New(g_appSettings.ResamplerType, int(track.Virtual.Format.Channels), int(float64(track.Virtual.Format.Channels)*resamplerSize))

	if resamplerError != nil {
		return resamplerError
	}

	track.Resampler = &resampler

	return nil
}

func (track *AudioTrack) ClearTrackSafe() {
	track.Data = nil

	if track.Device != nil {
		track.ClearDevice()
	}

	if track.Virtual != nil {
		track.ClearVirtual()
	}

	if track.Resampler != nil {
		track.ClearResampler()
	}

	if track.Binding != 0 {
		track.ClearBinding()
	}
}

func (track *AudioTrack) StartDevice(index int) error {
	var finalError error

	if finalError = track.Device.Start(); finalError == nil || finalError != malgo.ErrUnavailable {
		return finalError
	}

	deviceIndex := index

	if deviceIndex == -1 {
		for index := range g_initDevices {
			if g_initDevices[index] != track.Device {
				continue
			}

			deviceIndex = index

			break
		}
	}

	cleanAudio()

	finalError = initializeAudioContext()

	if finalError != nil {
		return finalError
	}

	finalError = retrieveDevicesList()

	if finalError != nil {
		return finalError
	}

	finalError = initializeDevices()

	if finalError != nil {
		return finalError
	}

	track.Device = g_initDevices[deviceIndex]

	return track.Device.Start()
}

func (track *AudioTrack) GetRow() int {
	if g_filteredList != nil {
		return track.Row
	}

	return track.ID
}

func (track *AudioTrack) Queue() {
	if len(g_audioQueue) == 0 && g_currentTrack == nil {
		tryPlaySound(track, g_selectedDevice)

		return
	}

	g_audioQueue = append(g_audioQueue, track)

	ui.QueueMain(func() { g_queueModel.RowInserted(0) })
}

func makeDownloaderTab() ui.Control {
	vContainer := ui.NewVerticalBox()
	vContainer.SetPadded(true)

	downloadForm := ui.NewForm()
	downloadForm.SetPadded(true)

	downloadGrid := ui.NewGrid()
	downloadGrid.SetPadded(true)

	urlEntry := ui.NewEntry()
	fileNameEntry := ui.NewEntry()

	sizeLimitGrid := ui.NewGrid()
	sizeLimitGrid.SetPadded(true)

	sizeLimitEntry := ui.NewEntry()
	sizeLimitEntry.SetText(strconv.FormatInt(g_appSettings.VideoLimit, 10))

	sizeLimitGrid.Append(sizeLimitEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	sizeLimitButton := ui.NewButton("Apply new video size limit")
	sizeLimitButton.OnClicked(func(b *ui.Button) {
		newSizeLimit, convError := strconv.ParseInt(sizeLimitEntry.Text(), 10, 32)

		if convError != nil {
			logToEntry(convError.Error())

			return
		}

		if newSizeLimit == g_appSettings.VideoLimit {
			return
		}

		g_appSettings.VideoLimit = newSizeLimit

		go trySaveSettings()
	})

	sizeLimitGrid.Append(sizeLimitButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	playAfterBox := ui.NewHorizontalBox()
	playAfterBox.SetPadded(true)

	playAfterCheck := ui.NewCheckbox("")

	playAfterBox.Append(playAfterCheck, false)

	downloadGrid.Append(urlEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	downloadButton := ui.NewButton("Download and convert video")
	downloadButton.OnClicked(func(b *ui.Button) {
		if g_appSettings.LastDirectory == "" {
			logToEntry("No audio directory found")

			return
		}

		videoId, idError := ExtractVideoID(urlEntry.Text())

		if idError != nil {
			logToEntry(idError.Error())

			return
		}

		var downloadCallback func(int) = nil

		if playAfterCheck.Checked() {
			downloadCallback = func(trackIndex int) {
				go playSound(g_tracksList[trackIndex], -1)
			}
		}

		go tryDownloadVideo(videoId, fileNameEntry.Text(), downloadCallback)
	})

	downloadGrid.Append(downloadButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	downloadForm.Append("URL :", downloadGrid, false)
	downloadForm.Append("File name :", fileNameEntry, false)
	downloadForm.Append("Size limit (MB) :", sizeLimitGrid, false)
	downloadForm.Append("Play after download :", playAfterBox, false)

	vContainer.Append(downloadForm, false)
	vContainer.Append(ui.NewHorizontalSeparator(), false)
	vContainer.Append(ui.NewLabel("Set the size limit to 0 to disable it"), false)

	return vContainer
}

func downloadVideo(videoId string, outputName string) (string, error) {
	appPath, pathError := exec.LookPath("./yt-dlp")

	if pathError != nil {
		appPath, pathError = exec.LookPath("yt-dlp")

		if pathError != nil {
			return "", pathError
		}
	}

	outputFile := outputName

	if outputFile == "" {
		outputFile = videoId
	}

	expectedFilePath := filepath.Join(filepath.FromSlash(g_appSettings.LastDirectory), outputFile)
	tempFilePath := expectedFilePath + ".webm"
	expectedFilePath += ".ogg"

	appBin := exec.Command(appPath,
		"-f", "ba[ext=webm]",
		"-o", filepath.Join(filepath.FromSlash(g_appSettings.LastDirectory), outputFile+".%(ext)s"),
		"-q",
		"--remux-video", "ogg",
		"--max-filesize", fmt.Sprintf("%dM", g_appSettings.VideoLimit),
		"--max-downloads", "1",
		"--force-overwrites",
		"--no-playlist",
		"--no-part",
		"--no-continue",
		"--no-cache-dir",
		"--no-mtime",
		"--", // https://www.gnu.org/software/libc/manual/html_node/Argument-Syntax.html
		videoId)
	appBin.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow}

	runError := appBin.Run()

	_, statError := os.Stat(expectedFilePath)

	if statError != nil {
		if _, tempError := os.Stat(tempFilePath); tempError == nil {
			os.Remove(tempFilePath)
		}

		if runError != nil {
			return "", runError
		}

		return "", statError
	}

	return expectedFilePath, nil
}

// https://github.com/kkdai/youtube/blob/master/video_id.go

var VideoRegexpList = []*regexp.Regexp{
	regexp.MustCompile(`(?:v|embed|shorts|watch\?v)(?:=|/)([^"&?/=%]{11})`),
	regexp.MustCompile(`(?:=|/)([^"&?/=%]{11})`),
	regexp.MustCompile(`([^"&?/=%]{11})`),
}

func ExtractVideoID(videoID string) (string, error) {
	if strings.Contains(videoID, "youtu") || strings.ContainsAny(videoID, "\"?&/<%=") {
		for _, re := range VideoRegexpList {
			if isMatch := re.MatchString(videoID); isMatch {
				subs := re.FindStringSubmatch(videoID)
				videoID = subs[1]
			}
		}
	}

	if strings.ContainsAny(videoID, "?&/<%=") {
		return "", errors.New("invalid characters in video id")
	}

	if len(videoID) < 10 {
		return "", errors.New("the video id must be at least 10 characters long")
	}

	return videoID, nil
}

func tryDownloadVideo(videoId string, outputName string, downloadCallback func(trackIndex int)) {
	var downloadError error
	var outputPath string

	if outputPath, downloadError = downloadVideo(videoId, outputName); downloadError != nil {
		ui.QueueMain(func() { logToEntry(downloadError.Error()) })

		return
	}

	found := false
	trackIndex := len(g_tracksList)

	for index := trackIndex - 1; index >= 0; index-- {
		if g_tracksList[index].Path != outputPath {
			continue
		}

		if g_tracksList[index] == g_currentTrack {
			g_currentTrack.ID = -1
		} else {
			g_tracksList[index].ClearTrackSafe()
		}

		g_tracksList[index] = &AudioTrack{
			ID:          index,
			Row:         index,
			Extension:   filepath.Ext(outputPath),
			Name:        strings.TrimSuffix(filepath.Base(outputPath), filepath.Ext(outputPath)),
			Path:        outputPath,
			Volume:      defaultVolume,
			Binding:     0,
			SampleRatio: -1,
			Data:        nil,
			ReadMode:    false,
			Virtual:     nil,
			Device:      nil,
			Resampler:   nil,
		}

		trackIndex = index
		found = true

		break
	}

	if !found {
		g_tracksList = append(g_tracksList, &AudioTrack{
			ID:          trackIndex,
			Row:         trackIndex,
			Extension:   filepath.Ext(outputPath),
			Name:        strings.TrimSuffix(filepath.Base(outputPath), filepath.Ext(outputPath)),
			Path:        outputPath,
			Volume:      defaultVolume,
			Binding:     0,
			SampleRatio: -1,
			Data:        nil,
			ReadMode:    false,
			Virtual:     nil,
			Device:      nil,
			Resampler:   nil,
		})

		ui.QueueMain(func() { g_filesTableModel.RowInserted(0) })
	}

	for index := range g_audioQueue {
		if g_audioQueue[index].Path != outputPath {
			continue
		}

		g_audioQueue[index].ID = -1

		break
	}

	if downloadCallback == nil {
		return
	}

	downloadCallback(trackIndex)
}

func makeLogWatchTab() ui.Control {
	vContainer := ui.NewVerticalBox()
	vContainer.SetPadded(true)

	regexForm := ui.NewForm()
	regexForm.SetPadded(true)

	commandPrefixGrid := ui.NewGrid()
	commandPrefixGrid.SetPadded(true)

	commandPrefixEntry := ui.NewEntry()
	commandPrefixEntry.SetText(g_appSettings.CommandPrefix)

	commandPrefixGrid.Append(commandPrefixEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	commandPrefixButton := ui.NewButton("Apply new command regex")
	commandPrefixButton.OnClicked(func(b *ui.Button) {
		newCommandPrefix := commandPrefixEntry.Text()

		if newCommandPrefix == g_appSettings.CommandPrefix {
			return
		}

		g_appSettings.CommandPrefix = newCommandPrefix

		var compileError error
		g_chatSeparatorRegex, compileError = regexp.Compile(defaultChatSeparator + g_appSettings.CommandPrefix)

		if compileError != nil {
			logToEntry(compileError.Error())

			g_appSettings.CommandPrefix = defaultCommandPrefix
			g_chatSeparatorRegex = regexp.MustCompile(defaultChatSeparator + g_appSettings.CommandPrefix)
			commandPrefixEntry.SetText(g_appSettings.CommandPrefix)

			return
		}

		go trySaveSettings()
	})

	commandPrefixGrid.Append(commandPrefixButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	regexForm.Append("Command prefix regex :", commandPrefixGrid, false)

	chatPrefixGrid := ui.NewGrid()
	chatPrefixGrid.SetPadded(true)

	chatPrefixEntry := ui.NewEntry()
	chatPrefixEntry.SetText(g_appSettings.ChatPrefix)

	chatPrefixGrid.Append(chatPrefixEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	chatPrefixButton := ui.NewButton("Apply new chat regex")
	chatPrefixButton.OnClicked(func(b *ui.Button) {
		newChatPrefix := chatPrefixEntry.Text()

		if newChatPrefix == g_appSettings.ChatPrefix {
			return
		}

		g_appSettings.ChatPrefix = newChatPrefix

		var compileError error
		g_chatPrefixRegex, compileError = regexp.Compile(g_appSettings.ChatPrefix)

		if compileError != nil {
			logToEntry(compileError.Error())

			g_appSettings.ChatPrefix = defaultChatPrefix
			g_chatPrefixRegex = regexp.MustCompile(g_appSettings.ChatPrefix)
			chatPrefixEntry.SetText(g_appSettings.ChatPrefix)

			return
		}

		go trySaveSettings()
	})

	chatPrefixGrid.Append(chatPrefixButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	regexForm.Append("Chat prefix regex :", chatPrefixGrid, false)

	hContainer := ui.NewHorizontalBox()
	hContainer.SetPadded(true)

	commandsGroup := ui.NewGroup("Commands")
	commandsGroup.SetMargined(true)

	commandsModel := ui.NewTableModel(&CommandsTableModel{})
	commandsTable := ui.NewTable(&ui.TableParams{
		Model:                         commandsModel,
		RowBackgroundColorModelColumn: 3,
	})
	commandsModel.RowInserted(0)

	commandsTable.AppendTextColumn("Command", 0, ui.TableModelColumnNeverEditable, &ui.TableTextColumnOptionalParams{ColorModelColumn: -1})
	commandsTable.AppendTextColumn("Description", 1, ui.TableModelColumnNeverEditable, &ui.TableTextColumnOptionalParams{ColorModelColumn: -1})
	commandsTable.AppendCheckboxColumn("Restrict to allowed users", 2, ui.TableModelColumnAlwaysEditable)

	commandsGroup.SetChild(commandsTable)

	hContainer.Append(commandsGroup, true)

	blockedGroup := ui.NewGroup("Blocked users")
	blockedGroup.SetMargined(true)

	blockedContainer := ui.NewVerticalBox()
	blockedContainer.SetPadded(true)

	g_blockedModel = ui.NewTableModel(&BlockedTableModel{})
	blockedTable := ui.NewTable(&ui.TableParams{
		Model:                         g_blockedModel,
		RowBackgroundColorModelColumn: 2,
	})

	blockedTable.AppendTextColumn("Name", 0, ui.TableModelColumnNeverEditable, &ui.TableTextColumnOptionalParams{ColorModelColumn: -1})
	blockedTable.AppendButtonColumn("Remove", 1, ui.TableModelColumnAlwaysEditable)

	blockedContainer.Append(blockedTable, true)

	blockedGrid := ui.NewGrid()
	blockedGrid.SetPadded(true)

	blockedEntry := ui.NewEntry()

	blockedGrid.Append(blockedEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	blockedButton := ui.NewButton("Block user")
	blockedButton.OnClicked(func(b *ui.Button) {
		g_appSettings.BlockedUsers = append(g_appSettings.BlockedUsers, blockedEntry.Text())

		g_blockedModel.RowInserted(0)

		go trySaveSettings()
	})

	blockedGrid.Append(blockedButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	blockedContainer.Append(blockedGrid, false)

	blockedGroup.SetChild(blockedContainer)

	hContainer.Append(blockedGroup, true)

	if len(g_appSettings.BlockedUsers) != 0 {
		g_blockedModel.RowInserted(0)
	}

	allowedGroup := ui.NewGroup("Allowed users")
	allowedGroup.SetMargined(true)

	allowedContainer := ui.NewVerticalBox()
	allowedContainer.SetPadded(true)

	g_allowedModel = ui.NewTableModel(&AllowedTableModel{})
	allowedTable := ui.NewTable(&ui.TableParams{
		Model:                         g_allowedModel,
		RowBackgroundColorModelColumn: 2,
	})

	allowedTable.AppendTextColumn("Name", 0, ui.TableModelColumnNeverEditable, &ui.TableTextColumnOptionalParams{ColorModelColumn: -1})
	allowedTable.AppendButtonColumn("Remove", 1, ui.TableModelColumnAlwaysEditable)

	allowedContainer.Append(allowedTable, true)

	allowedGrid := ui.NewGrid()
	allowedGrid.SetPadded(true)

	allowedEntry := ui.NewEntry()

	allowedGrid.Append(allowedEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	allowedButton := ui.NewButton("Allow user")
	allowedButton.OnClicked(func(b *ui.Button) {
		g_appSettings.AllowedUsers = append(g_appSettings.AllowedUsers, allowedEntry.Text())

		g_allowedModel.RowInserted(0)

		go trySaveSettings()
	})

	allowedGrid.Append(allowedButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	allowedContainer.Append(allowedGrid, false)

	allowedGroup.SetChild(allowedContainer)

	hContainer.Append(allowedGroup, true)

	if len(g_appSettings.AllowedUsers) != 0 {
		g_allowedModel.RowInserted(0)
	}

	saveHBox := ui.NewHorizontalBox()
	saveHBox.SetPadded(true)

	saveButton := ui.NewButton("Set log watch file")
	saveButton.OnClicked(func(b *ui.Button) {
		fileSave := ui.OpenFile(g_mainWindow)

		if fileSave == "" {
			return
		}

		fileSave = filepath.ToSlash(fileSave)

		if fileSave == g_appSettings.LogWatch {
			logToEntry("Ignored same log watch input file")

			return
		}

		if setError := setWatchFile(fileSave); setError != nil {
			logToEntry(setError.Error())

			return
		}

		go trySaveSettings()
	})

	saveHBox.Append(saveButton, false)

	timestampHBox := ui.NewHorizontalBox()
	timestampHBox.SetPadded(true)

	timestampCheckbox := ui.NewCheckbox("Timestamped log file")
	timestampCheckbox.SetChecked(g_appSettings.Timestamped)
	timestampCheckbox.OnToggled(func(c *ui.Checkbox) {
		g_appSettings.Timestamped = c.Checked()

		go trySaveSettings()
	})

	timestampHBox.Append(timestampCheckbox, false)

	if g_appSettings.LogWatch != "" {
		if setError := setWatchFile(g_appSettings.LogWatch); setError != nil {
			logToEntry(setError.Error())
		}
	}

	vContainer.Append(regexForm, false)
	vContainer.Append(hContainer, true)
	vContainer.Append(ui.NewLabel("Wildcard \"*\" (without quotes) gets triggered for any user"), false)
	vContainer.Append(saveHBox, false)
	vContainer.Append(timestampHBox, false)

	return vContainer
}

func (mh *CommandsTableModel) ColumnTypes(m *ui.TableModel) []ui.TableValue {
	return []ui.TableValue{
		ui.TableString(""),
		ui.TableString(""),
		ui.TableInt(0),
		ui.TableColor{},
	}
}

func (mh *CommandsTableModel) NumRows(m *ui.TableModel) int {
	return len(commandsList) - 1
}

func (mh *CommandsTableModel) CellValue(m *ui.TableModel, row, column int) ui.TableValue {
	switch column {
	case 0:
		return ui.TableString(commandsList[row])
	case 1:
		return ui.TableString(g_logCommands[commandsList[row]].Description)
	case 2:
		if g_logCommands[commandsList[row]].AllowedOnly {
			return ui.TableInt(1)
		}

		return ui.TableInt(0)
	case 3:
		if row%2 == 0 {
			return ui.TableColor{R: 0, G: 0, B: 0, A: 0.05}
		}

		return nil
	}

	return nil
}

func (mh *CommandsTableModel) SetCellValue(m *ui.TableModel, row, column int, value ui.TableValue) {
	switch column {
	case 2:
		commandToggle(row, int(value.(ui.TableInt)))

		go trySaveSettings()
	}
}

func (mh *BlockedTableModel) ColumnTypes(m *ui.TableModel) []ui.TableValue {
	return []ui.TableValue{
		ui.TableString(""),
		ui.TableString(""),
		ui.TableColor{},
	}
}

func (mh *BlockedTableModel) NumRows(m *ui.TableModel) int {
	if len(g_appSettings.BlockedUsers) == 0 {
		return 0
	}

	return len(g_appSettings.BlockedUsers) - 1
}

func (mh *BlockedTableModel) CellValue(m *ui.TableModel, row, column int) ui.TableValue {
	switch column {
	case 0:
		if len(g_appSettings.BlockedUsers) == 0 {
			return ui.TableString("")
		}

		return ui.TableString(g_appSettings.BlockedUsers[row])
	case 1:
		if len(g_appSettings.BlockedUsers) == 0 {
			return ui.TableString("")
		}

		return ui.TableString("Remove")
	case 2:
		return ui.TableColor{R: 1.0, G: 0, B: 0, A: 0.06}
	}

	return nil
}

func (mh *BlockedTableModel) SetCellValue(m *ui.TableModel, row, column int, value ui.TableValue) {
	if value == nil {
		switch column {
		case 1:
			if len(g_appSettings.BlockedUsers) == 0 {
				return
			}

			g_appSettings.BlockedUsers = append(g_appSettings.BlockedUsers[:row], g_appSettings.BlockedUsers[row+1:]...)
			m.RowInserted(0)

			go trySaveSettings()
		}

		return
	}
}

func (mh *AllowedTableModel) ColumnTypes(m *ui.TableModel) []ui.TableValue {
	return []ui.TableValue{
		ui.TableString(""),
		ui.TableString(""),
		ui.TableColor{},
	}
}

func (mh *AllowedTableModel) NumRows(m *ui.TableModel) int {
	if len(g_appSettings.AllowedUsers) == 0 {
		return 0
	}

	return len(g_appSettings.AllowedUsers) - 1
}

func (mh *AllowedTableModel) CellValue(m *ui.TableModel, row, column int) ui.TableValue {
	switch column {
	case 0:
		if len(g_appSettings.AllowedUsers) == 0 {
			return ui.TableString("")
		}

		return ui.TableString(g_appSettings.AllowedUsers[row])
	case 1:
		if len(g_appSettings.AllowedUsers) == 0 {
			return ui.TableString("")
		}

		return ui.TableString("Remove")
	case 2:
		return ui.TableColor{R: 0, G: 1.0, B: 0, A: 0.08}
	}

	return nil
}

func (mh *AllowedTableModel) SetCellValue(m *ui.TableModel, row, column int, value ui.TableValue) {
	if value == nil {
		switch column {
		case 1:
			if len(g_appSettings.AllowedUsers) == 0 {
				return
			}

			g_appSettings.AllowedUsers = append(g_appSettings.AllowedUsers[:row], g_appSettings.AllowedUsers[row+1:]...)
			m.RowInserted(0)

			go trySaveSettings()
		}

		return
	}
}

func setWatchFile(fileSave string) error {
	if fileSave == g_appSettings.LogFile {
		if g_watchFile == nil {
			g_appSettings.LogWatch = ""
		}

		return errors.New("SetWatchFile : Cannot use log output as input file")
	}

	if g_watchFile != nil {
		g_watchFile.Stop()
		g_watchFile = nil
	}

	var tailError error
	g_watchFile, tailError = tail.TailFile(filepath.FromSlash(fileSave),
		tail.Config{
			Follow:    true,
			Location:  &tail.SeekInfo{Offset: 0, Whence: io.SeekEnd},
			Poll:      true,
			MustExist: true,
		})

	if tailError != nil {
		return tailError
	}

	g_appSettings.LogWatch = fileSave

	go watchCallback()

	return nil
}

func watchCallback() {
	var playerName string
	var fullCommand string
	var separatorIndexes []int = nil

	var isAllowed bool = false
	var isBlocked bool = false

	for line := range g_watchFile.Lines {
		separatorIndexes = g_chatSeparatorRegex.FindStringIndex(line.Text)

		if separatorIndexes == nil {
			continue
		}

		playerName, fullCommand = line.Text[:separatorIndexes[0]], line.Text[separatorIndexes[1]:]

		separatorIndexes = nil

		if g_appSettings.Timestamped {
			separatorIndexes = g_timestampRegex.FindStringIndex(playerName)

			if separatorIndexes != nil {
				playerName = playerName[separatorIndexes[1]:]

				separatorIndexes = nil
			}
		}

		separatorIndexes = g_chatPrefixRegex.FindStringIndex(playerName)

		if separatorIndexes != nil {
			playerName = playerName[separatorIndexes[1]:]

			separatorIndexes = nil
		}

		isAllowed = false
		isBlocked = false

		for _, item := range g_appSettings.BlockedUsers {
			if item == "*" {
				isBlocked = true

				break
			}

			if playerName != item {
				continue
			}

			isBlocked = true

			break
		}

		for _, item := range g_appSettings.AllowedUsers {
			if item == "*" {
				isAllowed = true

				break
			}

			if playerName != item {
				continue
			}

			isAllowed = true

			break
		}

		firstSpace := strings.IndexByte(fullCommand, ' ')

		if firstSpace == -1 {
			command, exists := g_logCommands[strings.TrimSpace(fullCommand)]

			if !exists || (command.AllowedOnly && !isAllowed) || (isBlocked && !isAllowed) {
				continue
			}

			command.Action("")

			continue
		}

		command, exists := g_logCommands[fullCommand[0:firstSpace]]

		if !exists || (command.AllowedOnly && !isAllowed) || (isBlocked && !isAllowed) {
			continue
		}

		argument := strings.TrimSpace(fullCommand[firstSpace+1:])

		if argument == "" {
			continue
		}

		command.Action(argument)
	}
}

func playCommand(arg string) {
	if (arg == "") || (g_appSettings.QueueLimit != 0) && (len(g_audioQueue) >= g_appSettings.QueueLimit) {
		return
	}

	for _, item := range g_tracksList {
		if !strings.EqualFold(item.Name, arg) {
			continue
		}

		item.Queue()

		return
	}

	if trackIndex, parseError := strconv.ParseUint(arg, 10, 32); parseError == nil && (int(trackIndex) < len(g_tracksList)) {
		g_tracksList[trackIndex].Queue()

		return
	}
}

func forcePlayCommand(arg string) {
	if arg == "" {
		return
	}

	for _, item := range g_tracksList {
		if !strings.EqualFold(item.Name, arg) {
			continue
		}

		tryPlaySound(item, g_selectedDevice)

		return
	}

	if trackIndex, parseError := strconv.ParseUint(arg, 10, 32); parseError == nil && (int(trackIndex) < len(g_tracksList)) {
		tryPlaySound(g_tracksList[trackIndex], g_selectedDevice)

		return
	}
}

func setVolumeCommand(arg string) {
	if (arg == "") || (g_currentTrack == nil) {
		return
	}

	newVolume, parseError := strconv.ParseFloat(arg, 32)

	if parseError != nil {
		ui.QueueMain(func() { logToEntry(parseError.Error()) })

		return
	}

	if g_currentTrack.Volume == float32(newVolume) {
		return
	}

	g_currentTrack.Volume = float32(newVolume)

	g_appSettings.Tracks[filepath.ToSlash(g_currentTrack.Path)] = g_currentTrack

	if (newVolume == float64(defaultVolume)) && (g_currentTrack.Binding == 0) {
		delete(g_appSettings.Tracks, filepath.ToSlash(g_currentTrack.Path))
	}

	ui.QueueMain(func() { g_filesTableModel.RowChanged(g_currentTrack.GetRow()) })

	go trySaveSettings()
}

func setGlobalVolumeCommand(arg string) {
	if arg == "" {
		return
	}

	newGlobalVolume, convError := strconv.ParseFloat(arg, 32)

	if convError != nil {
		logToEntry(convError.Error())

		return
	}

	if newGlobalVolume == float64(g_appSettings.GlobalVolume) {
		return
	}

	g_appSettings.GlobalVolume = float32(newGlobalVolume)

	ui.QueueMain(func() {
		g_globalVolumeEntry.SetText(strconv.FormatFloat(float64(g_appSettings.GlobalVolume), 'f', 2, 32))
	})

	go trySaveSettings()
}

func setSampleRateCommand(arg string) {
	if arg == "" {
		return
	}

	newSampleRate, convError := strconv.ParseUint(arg, 10, 32)

	if convError != nil {
		logToEntry(convError.Error())

		return
	}

	if newSampleRate == uint64(g_appSettings.SampleRate) {
		return
	}

	ui.QueueMain(func() { updateSampleRate(uint32(newSampleRate)) })
	ui.QueueMain(func() {
		g_sampleRateEntry.SetText(strconv.FormatUint(uint64(g_appSettings.SampleRate), 10))
	})

	go trySaveSettings()
}

func ttsCommand(arg string) {
	if arg == "" {
		return
	}

	speakText(arg, g_selectedDevice)
}

func videoCommand(arg string) {
	if (arg == "") || (g_appSettings.QueueLimit != 0) && (len(g_audioQueue) >= g_appSettings.QueueLimit) {
		return
	}

	videoId, idError := ExtractVideoID(arg)

	if idError != nil {
		ui.QueueMain(func() { logToEntry(idError.Error()) })

		return
	}

	for _, item := range g_tracksList {
		if videoId != item.Name {
			continue
		}

		item.Queue()

		return
	}

	go func() {
		if g_appSettings.LastDirectory == "" {
			ui.QueueMain(func() { logToEntry("No audio directory found") })

			return
		}

		tryDownloadVideo(videoId, "",
			func(trackIndex int) {
				g_tracksList[trackIndex].Queue()
			})
	}()
}

func forceVideoCommand(arg string) {
	if arg == "" {
		return
	}

	videoId, idError := ExtractVideoID(arg)

	if idError != nil {
		ui.QueueMain(func() { logToEntry(idError.Error()) })

		return
	}

	for _, item := range g_tracksList {
		if videoId != item.Name {
			continue
		}

		tryPlaySound(item, g_selectedDevice)

		return
	}

	go func() {
		if g_appSettings.LastDirectory == "" {
			ui.QueueMain(func() { logToEntry("No audio directory found") })

			return
		}

		tryDownloadVideo(videoId, "",
			func(trackIndex int) {
				go playSound(g_tracksList[trackIndex], g_selectedDevice)
			})
	}()
}

func skipCommand(arg string) {
	if g_currentTrack == nil {
		return
	}

	if _, seekError := g_currentTrack.Virtual.Seek(0, sndfile.End); seekError != nil {
		ui.QueueMain(func() { logToEntry(seekError.Error()) })
	}
}

func skipAllCommand(arg string) {
	for index, item := range g_audioQueue {
		if item.ID != -1 || item == g_currentTrack {
			continue
		}

		item.ClearTrackSafe()
		g_audioQueue[index] = nil
	}

	g_audioQueue = nil

	ui.QueueMain(func() { g_queueModel.RowInserted(0) })

	if g_currentTrack == nil {
		return
	}

	if _, seekError := g_currentTrack.Virtual.Seek(0, sndfile.End); seekError != nil {
		ui.QueueMain(func() { logToEntry(seekError.Error()) })
	}
}

func allowCommand(arg string) {
	if arg == "" {
		return
	}

	g_appSettings.AllowedUsers = append(g_appSettings.AllowedUsers, arg)

	ui.QueueMain(func() { g_allowedModel.RowInserted(0) })

	go trySaveSettings()
}

func blockCommand(arg string) {
	if arg == "" {
		return
	}

	g_appSettings.BlockedUsers = append(g_appSettings.BlockedUsers, arg)

	ui.QueueMain(func() { g_blockedModel.RowInserted(0) })

	go trySaveSettings()
}

func removeAllowCommand(arg string) {
	if arg == "" {
		return
	}

	for index := 0; index < len(g_appSettings.AllowedUsers); {
		if g_appSettings.AllowedUsers[index] != arg {
			index++

			continue
		}

		g_appSettings.AllowedUsers = append(g_appSettings.AllowedUsers[:index], g_appSettings.AllowedUsers[index+1:]...)
		index = 0
	}

	ui.QueueMain(func() { g_allowedModel.RowInserted(0) })

	go trySaveSettings()
}

func removeBlockCommand(arg string) {
	if arg == "" {
		return
	}

	for index := 0; index < len(g_appSettings.BlockedUsers); {
		if g_appSettings.BlockedUsers[index] != arg {
			index++

			continue
		}

		g_appSettings.BlockedUsers = append(g_appSettings.BlockedUsers[:index], g_appSettings.BlockedUsers[index+1:]...)
		index = 0
	}

	ui.QueueMain(func() { g_blockedModel.RowInserted(0) })

	go trySaveSettings()
}

func commandToggle(row int, value int) {
	checkBoxState := false

	if value == 1 {
		checkBoxState = true
	}

	key := commandsList[row]

	g_logCommands[key].AllowedOnly = checkBoxState

	_, exists := g_appSettings.Commands[key]

	if exists {
		g_appSettings.Commands[key].AllowedOnly = checkBoxState

		if checkBoxState == permissionsMap[key] {
			g_appSettings.Commands[key] = nil
			delete(g_appSettings.Commands, key)
		}

		return
	}

	if checkBoxState == permissionsMap[key] {
		return
	}

	g_appSettings.Commands[key] = &LogCommand{checkBoxState, nil, ""}
}

func makeQueueTab() ui.Control {
	vContainer := ui.NewVerticalBox()
	vContainer.SetPadded(true)

	hContainer := ui.NewVerticalBox()
	hContainer.SetPadded(true)

	queueLimitForm := ui.NewForm()
	queueLimitForm.SetPadded(true)

	queueLimitGrid := ui.NewGrid()
	queueLimitGrid.SetPadded(true)

	queueLimitEntry := ui.NewEntry()
	queueLimitEntry.SetText(strconv.FormatInt(int64(g_appSettings.QueueLimit), 10))

	queueLimitGrid.Append(queueLimitEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	queueLimitButton := ui.NewButton("Apply new queue entries limit")
	queueLimitButton.OnClicked(func(b *ui.Button) {
		newQueueLimit, convError := strconv.ParseInt(queueLimitEntry.Text(), 10, 32)

		if convError != nil {
			logToEntry(convError.Error())

			return
		}

		if newQueueLimit == int64(g_appSettings.QueueLimit) {
			return
		}

		g_appSettings.QueueLimit = int(newQueueLimit)

		go trySaveSettings()
	})

	queueLimitGrid.Append(queueLimitButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	queueLimitForm.Append("Queue limit :", queueLimitGrid, false)

	hContainer.Append(ui.NewLabel("Set the entries limit to 0 to disable it"), false)
	hContainer.Append(queueLimitForm, false)

	g_queueModel = ui.NewTableModel(&QueueTableModel{})
	queueTable := ui.NewTable(&ui.TableParams{
		Model:                         g_queueModel,
		RowBackgroundColorModelColumn: 2,
	})

	queueTable.AppendTextColumn("Name", 0, ui.TableModelColumnNeverEditable, &ui.TableTextColumnOptionalParams{ColorModelColumn: -1})
	queueTable.AppendButtonColumn("Remove", 1, ui.TableModelColumnAlwaysEditable)

	hContainer.Append(queueTable, true)

	vContainer.Append(hContainer, true)

	return vContainer
}

func (mh *QueueTableModel) ColumnTypes(m *ui.TableModel) []ui.TableValue {
	return []ui.TableValue{
		ui.TableString(""),
		ui.TableString(""),
		ui.TableColor{},
	}
}

func (mh *QueueTableModel) NumRows(m *ui.TableModel) int {
	if len(g_audioQueue) == 0 {
		return 0
	}

	return len(g_audioQueue) - 1
}

func (mh *QueueTableModel) CellValue(m *ui.TableModel, row, column int) ui.TableValue {
	switch column {
	case 0:
		if len(g_audioQueue) == 0 {
			return ui.TableString("")
		}

		return ui.TableString(g_audioQueue[row].Name)
	case 1:
		if len(g_audioQueue) == 0 {
			return ui.TableString("")
		}

		return ui.TableString("Remove")
	case 2:
		if row%2 == 0 {
			return ui.TableColor{R: 0, G: 0, B: 0, A: 0.05}
		}

		return nil
	}

	return nil
}

func (mh *QueueTableModel) SetCellValue(m *ui.TableModel, row, column int, value ui.TableValue) {
	if value == nil {
		switch column {
		case 1:
			if len(g_audioQueue) == 0 {
				return
			}

			if g_audioQueue[row].ID == -1 && g_audioQueue[row] != g_currentTrack {
				g_audioQueue[row].ClearTrackSafe()
			}

			g_audioQueue[row] = nil
			g_audioQueue = append(g_audioQueue[:row], g_audioQueue[row+1:]...)
			m.RowInserted(0)
		}

		return
	}
}

func makeTTSTab() ui.Control {
	vContainer := ui.NewVerticalBox()
	vContainer.SetPadded(true)

	ttsForm := ui.NewForm()
	ttsForm.SetPadded(true)

	voicesComboBox := ui.NewCombobox()

	for _, item := range g_voicesList {
		voicesComboBox.Append(item)
	}

	voicesComboBox.SetSelected(g_selectedVoice)
	voicesComboBox.OnSelected(func(c *ui.Combobox) {
		selectedItem := c.Selected()

		if selectedItem == g_selectedVoice {
			return
		}

		g_selectedVoice = selectedItem
		g_appSettings.TTSVoice = g_voicesList[selectedItem]

		go trySaveSettings()
	})

	ttsForm.Append("TTS Voice :", voicesComboBox, false)

	voiceVolumeGrid := ui.NewGrid()
	voiceVolumeGrid.SetPadded(true)

	voiceVolumeEntry := ui.NewEntry()
	voiceVolumeEntry.SetText(strconv.FormatFloat(float64(g_appSettings.TTSVolume), 'f', 2, 32))

	voiceVolumeGrid.Append(voiceVolumeEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	voiceVolumeButton := ui.NewButton("Apply new TTS volume")
	voiceVolumeButton.OnClicked(func(b *ui.Button) {
		newTTSVolume, parseError := strconv.ParseFloat(voiceVolumeEntry.Text(), 32)

		if parseError != nil {
			logToEntry(parseError.Error())

			return
		}

		if float32(newTTSVolume) == g_appSettings.TTSVolume {
			return
		}

		if newTTSVolume > 100 || newTTSVolume < 0 {
			voiceVolumeEntry.SetText(strconv.FormatFloat(defaultTTSVolume, 'f', 2, 32))

			logToEntry("Volume must be between 0 and 100")

			return
		}

		g_appSettings.TTSVolume = float32(newTTSVolume)
		setTTSVolume(g_appSettings.TTSVolume)

		go trySaveSettings()
	})

	voiceVolumeGrid.Append(voiceVolumeButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	ttsForm.Append("Volume :", voiceVolumeGrid, false)

	voiceRateGrid := ui.NewGrid()
	voiceRateGrid.SetPadded(true)

	voiceRateEntry := ui.NewEntry()
	voiceRateEntry.SetText(strconv.FormatFloat(float64(g_appSettings.TTSRate), 'f', 2, 32))

	voiceRateGrid.Append(voiceRateEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	voiceRateButton := ui.NewButton("Apply new TTS rate")
	voiceRateButton.OnClicked(func(b *ui.Button) {
		newTTSRate, parseError := strconv.ParseFloat(voiceRateEntry.Text(), 32)

		if parseError != nil {
			logToEntry(parseError.Error())

			return
		}

		if float32(newTTSRate) == g_appSettings.TTSRate {
			return
		}

		if newTTSRate > 10 || newTTSRate < -10 {
			voiceRateEntry.SetText(strconv.FormatFloat(defaultTTSRate, 'f', 2, 32))

			logToEntry("Rate must be between -10 and 10")

			return
		}

		g_appSettings.TTSRate = float32(newTTSRate)
		setTTSRate(g_appSettings.TTSRate)

		go trySaveSettings()
	})

	voiceRateGrid.Append(voiceRateButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	ttsForm.Append("Rate :", voiceRateGrid, false)

	speakGrid := ui.NewGrid()
	speakGrid.SetPadded(true)

	speakEntry := ui.NewEntry()

	speakGrid.Append(speakEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	speakButton := ui.NewButton("Speak")
	speakButton.OnClicked(func(b *ui.Button) {
		ttsText := speakEntry.Text()

		if ttsText == "" {
			return
		}

		speakText(ttsText, g_defaultDevice)
	})

	speakGrid.Append(speakButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	vContainer.Append(ttsForm, false)
	vContainer.Append(speakGrid, false)

	return vContainer
}

func speakText(text string, device int) {
	if g_ttsInPipe == nil {
		return
	}

	g_ttsInPipe.Write([]byte(fmt.Sprintf("\r\nSpeakText %d %d %s\r\n", g_selectedVoice, device, text)))
}

func setTTSVolume(newVolume float32) {
	if g_ttsInPipe == nil {
		return
	}

	g_ttsInPipe.Write([]byte(fmt.Sprintf("\r\nSetVolume %.2f\r\n", newVolume)))
}

func setTTSRate(newRate float32) {
	if g_ttsInPipe == nil {
		return
	}

	g_ttsInPipe.Write([]byte(fmt.Sprintf("\r\nSetRate %.2f\r\n", newRate)))
}

func makeLimiterTab() ui.Control {
	vContainer := ui.NewVerticalBox()
	vContainer.SetPadded(true)

	limiterForm := ui.NewForm()
	limiterForm.SetPadded(true)

	limiterGrid := ui.NewGrid()
	limiterGrid.SetPadded(true)

	limiterEntry := ui.NewEntry()
	limiterEntry.SetText(strconv.FormatFloat(float64(g_appSettings.LimiterThreshold), 'f', 2, 32))

	limiterGrid.Append(limiterEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	limiterButton := ui.NewButton("Apply new threshold")
	limiterButton.OnClicked(func(b *ui.Button) {
		newLimitValue, convError := strconv.ParseFloat(limiterEntry.Text(), 32)

		if convError != nil {
			logToEntry(convError.Error())

			return
		}

		if newLimitValue == float64(g_appSettings.LimiterThreshold) {
			return
		}

		g_appSettings.LimiterThreshold = float32(newLimitValue)

		if g_audioLimiter != nil {
			g_audioLimiter.Threshold = g_appSettings.LimiterThreshold
		}

		go trySaveSettings()
	})

	limiterGrid.Append(limiterButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	limiterForm.Append("Threshold value (+/- dB) :", limiterGrid, false)

	attackTimeGrid := ui.NewGrid()
	attackTimeGrid.SetPadded(true)

	attackTimeEntry := ui.NewEntry()
	attackTimeEntry.SetText(strconv.FormatFloat(float64(g_appSettings.AttackTime), 'f', 2, 32))

	attackTimeGrid.Append(attackTimeEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	attackTimeButton := ui.NewButton("Apply new attack time")
	attackTimeButton.OnClicked(func(b *ui.Button) {
		newAttackTime, convError := strconv.ParseFloat(attackTimeEntry.Text(), 32)

		if convError != nil {
			logToEntry(convError.Error())

			return
		}

		if newAttackTime == float64(g_appSettings.AttackTime) {
			return
		}

		g_appSettings.AttackTime = float32(newAttackTime)

		go trySaveSettings()
	})

	attackTimeGrid.Append(attackTimeButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	limiterForm.Append("Attack time (ms) :", attackTimeGrid, false)

	releaseTimeGrid := ui.NewGrid()
	releaseTimeGrid.SetPadded(true)

	releaseTimeEntry := ui.NewEntry()
	releaseTimeEntry.SetText(strconv.FormatFloat(float64(g_appSettings.ReleaseTime), 'f', 2, 32))

	releaseTimeGrid.Append(releaseTimeEntry, 0, 0, 1, 1, true, ui.AlignFill, false, ui.AlignCenter)

	releaseTimeButton := ui.NewButton("Apply new release time")
	releaseTimeButton.OnClicked(func(b *ui.Button) {
		newReleaseTime, convError := strconv.ParseFloat(releaseTimeEntry.Text(), 32)

		if convError != nil {
			logToEntry(convError.Error())

			return
		}

		if newReleaseTime == float64(g_appSettings.ReleaseTime) {
			return
		}

		g_appSettings.ReleaseTime = float32(newReleaseTime)

		go trySaveSettings()
	})

	releaseTimeGrid.Append(releaseTimeButton, 1, 0, 1, 1, false, ui.AlignFill, false, ui.AlignCenter)

	limiterForm.Append("Release time (ms) :", releaseTimeGrid, false)

	vContainer.Append(limiterForm, false)

	return vContainer
}

func (audioCompressor *Compressor) Compress(input float32) float32 {
	audioCompressor.PeakAvg = AttRAverage(audioCompressor.PeakAvg, audioCompressor.PeakAtTime, audioCompressor.PeakRTime, float32(math.Abs(float64(input))))
	gain := Limiter(audioCompressor.PeakAvg, audioCompressor.Threshold)
	audioCompressor.GainAvg = AttRAverage(audioCompressor.GainAvg, audioCompressor.GainRTime, audioCompressor.GainAtTime, gain)
	return input * audioCompressor.GainAvg
}

func AttRAverage(average float32, attackTime float32, releaseTime float32, input float32) float32 {
	tau := releaseTime

	if input > average {
		tau = attackTime
	}

	return ((1.0-tau)*average + (tau * input))
}

func Limiter(input float32, threshold float32) float32 {
	decibels := 20.0 * math.Log10(math.Abs(float64(input)))
	gain := math.Min(float64(threshold)-decibels, 0.0)
	return float32(math.Pow(10, 0.05*gain))
}

func CalcTau(samplerate uint32, timeMs float32) float32 {
	return float32(1.0 - math.Exp(float64(-2200.0/(timeMs*float32(samplerate)))))
}
