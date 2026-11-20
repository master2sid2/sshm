# Demo recording for the README. Regenerate with "make demo" (needs Docker):
# it starts a throwaway SSH server (docs/demo/server.sh) as "web-01" and
# imports the made-up hosts of docs/demo/ssh/ into a temporary HOME.

Output docs/demo/sshm.gif

Set Shell "bash"
Set FontSize 14
Set Width 1100
Set Height 640
Set Padding 18
Set Theme "Dracula"
Set TypingSpeed 60ms
Set Framerate 24

# TERM=screen-256color keeps the TUI from querying the terminal's colours:
# the answer arrives as input in VHS and would move the cursor.

Hide
Type "export PATH=$PWD:$PATH HOME=/tmp/demo PS1='$ ' D=$PWD TERM=screen-256color COLORTERM=truecolor; mkdir -p $HOME/.ssh && cp -r $D/docs/demo/ssh/. $HOME/.ssh/ && cp /keys/id_ed25519 $HOME/.ssh/ && chmod 600 $HOME/.ssh/id_ed25519 && cd $HOME && clear" Enter
Sleep 1s
Show

# First run: a new vault, and the hosts of ~/.ssh/config imported.
Type "sshm" Sleep 400ms Enter
Sleep 1.5s
Type "demo-password" Sleep 300ms Enter
Sleep 800ms
Type "demo-password" Sleep 300ms Enter
Sleep 2s
Type "y" Sleep 300ms Enter
Sleep 3s

# Open a folder, pick a host, connect: the new host key is confirmed once.
Enter Sleep 1.5s
Down Sleep 1.8s
Enter Sleep 2.5s
Type "yes" Sleep 300ms Enter
Sleep 2s
Type "hostname && whoami" Sleep 300ms Enter
Sleep 2s
Type "exit" Sleep 300ms Enter
Sleep 2.5s

# Back where we were; search finds hosts in every folder.
Type "/" Sleep 500ms
Type "db" Sleep 2.5s
Down Sleep 1.8s
Escape Sleep 800ms
Escape Sleep 1.5s
Type "q" Sleep 1s
