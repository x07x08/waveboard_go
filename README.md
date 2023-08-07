The Text-To-Speech and keyboard hooks are Windows only, thus this program compiles and works properly only on Windows.

# Features

* Global keyboard hotkeys for sounds (limited to only one key per sound)
* Real time playback. No separate files are stored on disk
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
			<img src="https://github.com/x07x08/waveboard/assets/88050465/2f3ab9da-c4da-4aba-a6f3-151e1dd66972" width = "100%" height = "100%">
			<p>
				Log tab
			</p>
		</td>
		<td align = "center">
			<img src="https://github.com/x07x08/waveboard/assets/88050465/6f42056d-e946-485b-851a-ca2736b9cd13" width = "100%" height = "100%">
			<p>
				Audio tab
			</p>
		</td>
	</tr>
	<tr>
		<td align = "center">
			<img src="https://github.com/x07x08/waveboard/assets/88050465/e0086bd8-fb1e-4f79-8446-ed52fa0a2b01" width = "100%" height = "100%">
			<p>
				Downloader tab
			</p>
		</td>
		<td align = "center">
			<img src="https://github.com/x07x08/waveboard/assets/88050465/cb8aedd6-2ba6-43e0-ace0-22d15bf372fe" width = "100%" height = "100%">
			<p>
				Log watch tab
			</p>
		</td>
	</tr>
	<tr>
		<td align = "center">
			<img src="https://github.com/x07x08/waveboard/assets/88050465/337bfbe7-4005-401c-bb67-4da0556f472d" width = "100%" height = "100%">
			<p>
				Queue tab
			</p>
		</td>
		<td align = "center">
			<img src="https://github.com/x07x08/waveboard/assets/88050465/cf63ac65-e201-4749-ac6d-76974acc1916" width = "100%" height = "100%">
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

4. <sup>*Optional*</sup> A ffmpeg binary for video downloading and conversion

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

* A C compiler for CGO. I have used [this](https://jmeubank.github.io/tdm-gcc/) one.
* [pkg-config](https://stackoverflow.com/questions/1710922/how-to-install-pkg-config-in-windows) for [libsndfile](https://github.com/libsndfile/libsndfile) / [gosndfile](https://github.com/mkb218/gosndfile) and [libsamplerate](https://github.com/libsndfile/libsamplerate) / [gosamplerate](https://github.com/dh1tw/gosamplerate)

# Credits

* [STARK](https://github.com/axynos/STARK) - I've used it for a long time, but it was half broken and used the disk to write raw audio data.
