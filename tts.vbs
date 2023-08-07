Option Explicit

Dim SAPIObject : Set SAPIObject = CreateObject("SAPI.SpVoice")
Dim VoiceTokens : Set VoiceTokens = SAPIObject.GetVoices()
Dim VoiceOutputs : Set VoiceOutputs = SAPIObject.GetAudioOutputs()
Dim StdIn : Set StdIn = CreateObject("Scripting.FileSystemObject").GetStandardStream(0)
Dim StdOut : Set StdOut = CreateObject("Scripting.FileSystemObject").GetStandardStream(1)

Dim VoiceVolume : VoiceVolume = 100
Dim VoiceRate : VoiceRate = 0.0

Sub ListVoices()
	Dim Voice
	For Each Voice in VoiceTokens
		StdOut.WriteLine(Voice.GetDescription)
	Next
	StdOut.WriteLine("End of voices list")
End Sub

Sub ListDevices()
	Dim Device
	For Each Device in VoiceOutputs
		StdOut.WriteLine(Device.GetDescription)
	Next
	StdOut.WriteLine("End of devices list")
End Sub

Sub SpeakText(VoiceIndex, DeviceIndex, Text)
	Set SAPIObject.AudioOutput = VoiceOutputs(DeviceIndex)
	Set SAPIObject.Voice = VoiceTokens(VoiceIndex)
	SAPIObject.Volume = VoiceVolume
	SAPIObject.Rate = VoiceRate
	SAPIObject.Speak Text, 1
End Sub

Sub SetVolume(NewVolume)
	Dim NumVolume : NumVolume = CLng(NewVolume)
	If NOT(NumVolume > 100 OR NumVolume < 0) Then
		VoiceVolume = NumVolume
	Else
		VoiceVolume = 100
	End If
End Sub

Sub SetRate(NewRate)
	Dim NumRate : NumRate = CLng(NewRate)
	If NOT(NumRate > 10 OR NumRate < -10) Then
		VoiceRate = NumRate
	Else
		VoiceRate = 0.0
	End If
End Sub

Sub MainLoop()
	Dim Input
	Dim Arguments
	
	Do
		Input = StdIn.ReadLine
		Arguments = Split(Input, " ", 4, 1)
		
		If uBound(Arguments) = -1 Then
			ReDim Arguments(1)
		End If
		
		If Arguments(0) = "SpeakText" AND uBound(Arguments) = 3 Then
			SpeakText Arguments(1), Arguments(2), Arguments(3)
		ElseIf Arguments(0) = "ListVoices" Then
			ListVoices
		ElseIf Arguments(0) = "SetVolume" AND uBound(Arguments) = 1 Then
			SetVolume Arguments(1)
		ElseIf Arguments(0) = "SetRate" AND uBound(Arguments) = 1 Then
			SetRate Arguments(1)
		ElseIf Arguments(0) = "ListDevices" Then
			ListDevices
		End If
	Loop While Not StdIn.AtEndOfStream
End Sub

MainLoop
