The Text-To-Speech and keyboard hooks are Windows only, thus this program compiles and works properly only on Windows.

# Features

* Global keyboard hotkeys for sounds (limited to only one key per sound)
* In-memory playback. No separate files are stored on disk
* Memory caching. The original file is accessed only once
* Source Engine chat commands
* Whitelist and blacklist (or just whitelist if everyone is blacklisted)
* Audio queue
* Video downloader and converter
* Text-To-Speech using [`SAPI`](https://learn.microsoft.com/en-us/previous-versions/windows/desktop/ms720592(v=vs.85))

# Issues

* User interface elements render slowly and flicker. This is an issue with the graphics library itself.
* There might be crashes.

# Screenshots

<table>
	<tr>
		<td align = "center">
			<img src="https://github.com/x07x08/waveboard/assets/88050465/14f396d6-ca54-4569-9aa6-09ea69dc6532" width = "100%" height = "100%">
			<p>
				Log tab
			</p>
		</td>
		<td align = "center">
			<img src="https://github.com/x07x08/waveboard/assets/88050465/a9738616-fd34-4b3a-9a98-3910f2196616" width = "100%" height = "100%">
			<p>
				Audio tab
			</p>
		</td>
	</tr>
	<tr>
		<td align = "center">
			<img src="https://github.com/x07x08/waveboard/assets/88050465/7ccedb30-7281-478d-b025-66803ecbbde4" width = "100%" height = "100%">
			<p>
				Downloader tab
			</p>
		</td>
		<td align = "center">
			<img src="https://github.com/x07x08/waveboard/assets/88050465/62e8b005-c983-459d-9d02-bd0c7d26bea9" width = "100%" height = "100%">
			<p>
				Log watch tab
			</p>
		</td>
	</tr>
	<tr>
		<td align = "center">
			<img src="https://github.com/x07x08/waveboard/assets/88050465/3acd787d-50ab-4de2-9d48-102a96175e67" width = "100%" height = "100%">
			<p>
				Queue tab
			</p>
		</td>
		<td align = "center">
			<img src="https://github.com/x07x08/waveboard/assets/88050465/ea1f4586-08e3-46c5-b9ac-d928bef40773" width = "100%" height = "100%">
			<p>
				Text-To-Speech tab
			</p>
		</td>
	</tr>
</table>

# Requirements

1. A virtual audio cable ([`Virtual-Audio-Cable`](https://vac.muzychenko.net) or [`VB-Audio Cable`](https://vb-audio.com/Cable/))
   - Follow [this](https://www.youtube.com/watch?v=fi5I6bzy2f8), [the text](https://github.com/fuck-shithub/STARK#how-to-set-up) tutorial or find another tutorial to install the drivers.
   - You may need to disable ["Driver Signature Enforcement"](https://www.youtube.com/watch?v=71YAIw7_-kg) during the installation process.

2. An audio directory (it is searched recursively)
   - It will be used by the program to play tracks and download videos.

3. <sup>*Optional*</sup> A log file within your Source Engine game using the [`con_logfile <file>`](https://developer.valvesoftware.com/wiki/List_of_console_scripting_commands) command or the [`-condebug`](https://developer.valvesoftware.com/wiki/Command_line_options) launch option
   - This is for the log watching feature, which allows interaction with the application using the game's chat.
   - You can use an `autoexec.cfg` file for easier management.

4. <sup>*Optional*</sup> yt-dlp and ffmpeg binaries for video downloading and conversion

# The "fixes" folder

It contains changes made to the following packages :

1. gosamplerate
   - The [`Process()`](https://github.com/dh1tw/gosamplerate/blob/e90cbce50defd16bdfd48e78b6288d2e0e7cccbb/gosamplerate.go#L172) method now returns the underlying array instead of allocating memory for each audio chunk.

2. gosndfile
   - All [`VirtualIo`](https://github.com/mkb218/gosndfile/blob/e0c9ef895ee23c154b6fe25b5261daf514df9941/sndfile/virtual.go#L46) function fields now work properly and don't crash.

3. [ui](https://github.com/aggyomfg/ui) - forked from [here](https://github.com/andlabs/ui)
   - Added proper menu support from [this](https://github.com/Nv7-GitHub/ui) fork (which seems to be identical to and predated by [this](https://github.com/jonhermansen/ui/commit/d0dea7122b6662e63bd3a6892a7bc8622dff4f76) fork).
   - Added resizing support from [here](https://github.com/ProtonMail/ui/commit/205a3d77a479211bdb63502eda53de2139ecc667), alongside the missing resize callback function.
   - Added fullscreen support.

# Compiling

You will need :

* A C compiler for CGO. I have used [this](https://github.com/niXman/mingw-builds-binaries/releases/tag/13.2.0-rt_v11-rev1) one (only [`msvcrt`](https://www.msys2.org/docs/environments/#msvcrt-vs-ucrt) builds work).
* [libsndfile](https://github.com/libsndfile/libsndfile) and [libsamplerate](https://github.com/libsndfile/libsamplerate).

# Credits

* [STARK](https://github.com/axynos/STARK) - I've used it for a long time, but it was half broken and used the disk to write raw audio data.
