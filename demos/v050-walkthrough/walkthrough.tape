# VHS tape for the v0.5.0 walk-through. Run from the repository root with a port reserved with portwarden exported
# as PORT: PORT=20001 vhs demos/v050-walkthrough/walkthrough.tape   (see README.md).
Output "demos/v050-walkthrough/out/mash-v050-walkthrough.mp4"
Output "demos/v050-walkthrough/out/mash-v050-walkthrough.gif"

Set Shell "bash"
Set FontSize 18
Set Width 1600
Set Height 960
Set Padding 24
Set Theme "Catppuccin Mocha"
Set TypingSpeed 0

Hide
Type "bash demos/v050-walkthrough/drive.sh --narrate --port $PORT --out demos/v050-walkthrough/out/narrate-report.md"
Enter
Wait+Screen@600s /READY/
Show
Wait+Screen@1800s /WALK-DONE/
Sleep 12s
