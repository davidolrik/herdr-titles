package main

// Nerd Font glyph map for tab naming, ported from icons.sh of
// qu8n/herdr-automatic-rename (MIT, (c) Quan Nguyen), whose map is in turn the
// icons block of joshmedeski/tmux-nerd-font-window-name plus that plugin's own
// aliases and a robot glyph for every agent herdr detects.
//
// The glyphs are Private Use Area characters and render as blank boxes without
// a Nerd Font. They are written as explicit escapes (generated mechanically
// from the upstream octal table) so a stripped or re-encoded glyph is visible
// in review; naming_test.go asserts exact codepoints, so a silent loss fails
// the suite (that exact accident shipped upstream once).

var builtinIcons = map[string]string{}

func init() {
	groups := []struct {
		glyph    string
		programs []string
	}{
		{"\uE73C", []string{"Python", "ipython", "ipython3", "pip", "pip3", "python", "python3"}}, // U+E73C
		{"\U000F07D4", []string{"R"}},                                           // U+F07D4 nf-md-language_r
		{"\U000F06A9", []string{"aider", "claude", "codex", "pi", "gemini"}},    // U+F06A9
		{"\U000F06A9", []string{"cursor", "cursor-agent", "devin", "cline"}},    // U+F06A9
		{"\U000F06A9", []string{"agy", "antigravity", "omp", "mastracode"}},     // U+F06A9
		{"\U000F06A9", []string{"opencode", "copilot", "kimi", "droid", "amp"}}, // U+F06A9
		{"\U000F06A9", []string{"kiro", "kiro-cli", "grok", "hermes", "kilo"}},  // U+F06A9
		{"\U000F06A9", []string{"qodercli"}},                                    // U+F06A9
		{"\uF120", []string{"alacritty", "gnome-terminal", "iterm2"}},           // U+F120
		{"\U000F109A", []string{"ansible", "ansible-playbook"}},                 // U+F109A nf-md-ansible
		{"\uE760", []string{"ant"}},                                             // U+E760
		{"\uF0AC", []string{"apache2", "httpd", "nginx"}},                       // U+F0AC
		{"\uE77D", []string{"apt", "dpkg", "nala"}},                             // U+E77D
		{"\uE764", []string{"atom"}},                                            // U+E764
		{"\uF270", []string{"aws"}},                                             // U+F270
		{"\uE70D", []string{"babel"}},                                           // U+E70D
		{"\uE795", []string{"bash", "fish", "just", "nu", "tcsh", "zsh"}},       // U+E795
		{"\U000F0B5F", []string{"bat"}},                                         // U+F0B5F
		{"\uE63A", []string{"bazel"}},                                           // U+E63A
		{"\uE7B1", []string{"beam", "beam.smp"}},                                // U+E7B1
		{"\uE703", []string{"bitbucket"}},                                       // U+E703
		{"\uEB29", []string{"brew"}},                                            // U+EB29
		{"\uEBA2", []string{"btm", "btop", "glances", "htop", "mactop", "top"}}, // U+EBA2
		{"\uE718", []string{"bun", "node", "npx", "pnpm", "yarn"}},              // U+E718
		{"\uF0F4", []string{"caffeinate"}},                                      // U+F0F4
		{"\uE7A8", []string{"cargo", "rustc", "rustup"}},                        // U+E7A8
		{"\uF0A0", []string{"cfdisk", "fdisk", "parted"}},                       // U+F0A0
		{"\uE61E", []string{"clang", "gcc"}},                                    // U+E61E
		{"\uE7B5", []string{"clion", "idea", "pycharm"}},                        // U+E7B5
		{"\uE624", []string{"cmake", "julia", "make"}},                          // U+E624
		{"\uE796", []string{"code", "code-insiders"}},                           // U+E796
		{"\uE783", []string{"composer"}},                                        // U+E783
		{"\U000F07B7", []string{"console"}},                                     // U+F07B7
		{"\uF073", []string{"crontab"}},                                         // U+F073
		{"\uE7AF", []string{"csharp"}},                                          // U+E7AF
		{"\uF019", []string{"curl", "wget"}},                                    // U+F019
		{"\uE798", []string{"dart", "flutter"}},                                 // U+E798
		{"\uEB52", []string{"deno"}},                                            // U+EB52
		{"\uF30A", []string{"dnf", "yum"}},                                      // U+F30A
		{"\uF308", []string{"docker", "lazydocker"}},                            // U+F308
		{"\uF481", []string{"doctl"}},                                           // U+F481
		{"\uE77F", []string{"dotnet"}},                                          // U+E77F
		{"\uE7B0", []string{"eclipse"}},                                         // U+E7B0
		{"\uE62D", []string{"elixir"}},                                          // U+E62D
		{"\uE632", []string{"emacs"}},                                           // U+E632
		{"\uE787", []string{"firebase"}},                                        // U+E787
		{"\uE270", []string{"gcloud"}},                                          // U+E270
		{"\uF188", []string{"gdb", "lldb", "valgrind"}},                         // U+F188
		{"\uE709", []string{"gh", "gitlab", "wordpress"}},                       // U+E709
		{"\uE777", []string{"ghc", "stack"}},                                    // U+E777
		{"\uEEFE", []string{"ghostty"}},                                         // U+EEFE
		{"\uE702", []string{"git", "gitui", "lazygit", "tig"}},                  // U+E702
		{"\uE627", []string{"go"}},                                              // U+E627
		{"\uF084", []string{"gpg"}},                                             // U+F084
		{"\uE714", []string{"gping", "ping"}},                                   // U+E714
		{"\uE7A9", []string{"gradle"}},                                          // U+E7A9
		{"\uE611", []string{"grunt"}},                                           // U+E611
		{"\uE610", []string{"gulp"}},                                            // U+E610
		{"\uE62B", []string{"gvim", "lvim", "vi", "view", "vim"}},               // U+E62B
		{"\U000F10FE", []string{"helm", "k9s", "kubectl", "kubie", "minikube"}}, // U+F10FE
		{"\uE749", []string{"heroku"}},                                          // U+E749
		{"\uE727", []string{"hg"}},                                              // U+E727
		{"\U000F0524", []string{"hx"}},                                          // U+F0524
		{"\uE256", []string{"java"}},                                            // U+E256
		{"\uE630", []string{"jekyll"}},                                          // U+E630
		{"\uE767", []string{"jenkins"}},                                         // U+E767
		{"\uE752", []string{"jest"}},                                            // U+E752
		{"\uE725", []string{"jj", "lazyjj", "svn"}},                             // U+E725
		{"\uE73F", []string{"laravel"}},                                         // U+E73F
		{"\uF07C", []string{"lf", "lfcd", "ranger"}},                            // U+F07C
		{"\uE7B4", []string{"maven"}},                                           // U+E7B4
		{"\uE79E", []string{"mocha"}},                                           // U+E79E
		{"\uE7A4", []string{"mongo"}},                                           // U+E7A4
		{"\uE704", []string{"mysql"}},                                           // U+E704
		{"\uF040", []string{"nano"}},                                            // U+F040
		{"\uE768", []string{"netbeans"}},                                        // U+E768
		{"\uE753", []string{"ng"}},                                              // U+E753
		{"\uE71E", []string{"npm"}},                                             // U+E71E
		{"\uE6AE", []string{"nvim"}},                                            // U+E6AE
		{"\uF023", []string{"openssl"}},                                         // U+F023
		{"\uF303", []string{"pacman", "paru", "yay"}},                           // U+F303
		{"\uE769", []string{"perl"}},                                            // U+E769
		{"\uE73D", []string{"php"}},                                             // U+E73D
		{"\uEBC7", []string{"powershell"}},                                      // U+EBC7
		{"\uE76E", []string{"psql"}},                                            // U+E76E
		{"\uF499", []string{"puppet"}},                                          // U+F499
		{"\uE7BA", []string{"react"}},                                           // U+E7BA
		{"\uE76D", []string{"redis"}},                                           // U+E76D
		{"\uF021", []string{"rsync"}},                                           // U+F021
		{"\uE23E", []string{"ruby"}},                                            // U+E23E
		{"\uE737", []string{"scala"}},                                           // U+E737
		{"\U000F08C0", []string{"scp", "ssh"}},                                  // U+F08C0
		{"\uEBC8", []string{"screen", "tmux"}},                                  // U+EBC8
		{"\uF1C0", []string{"sqlite"}},                                          // U+F1C0
		{"\uE7AA", []string{"sublime_text"}},                                    // U+E7AA
		{"\uF132", []string{"sudo"}},                                            // U+F132
		{"\uE755", []string{"swift"}},                                           // U+E755
		{"\uF085", []string{"systemctl"}},                                       // U+F085
		{"\U000F1062", []string{"terraform"}},                                   // U+F1062 nf-md-terraform
		{"\uEBE2", []string{"tickrs"}},                                          // U+EBE2
		{"\U000F06B0", []string{"topgrade"}},                                    // U+F06B0
		{"\uE77E", []string{"travis"}},                                          // U+E77E
		{"\uE628", []string{"tsc"}},                                             // U+E628
		{"\U000F15C3", []string{"unicorn"}},                                     // U+F15C3
		{"\uF1C6", []string{"unzip", "zip"}},                                    // U+F1C6
		{"\uF2B8", []string{"vagrant"}},                                         // U+F2B8
		{"\uE72A", []string{"virtualbox"}},                                      // U+E72A
		{"\uE70C", []string{"visualstudio"}},                                    // U+E70C
		{"\U000F0844", []string{"vue"}},                                         // U+F0844 nf-md-vuejs
		{"\uE770", []string{"webpack"}},                                         // U+E770
		{"\U000F0B79", []string{"weechat"}},                                     // U+F0B79
		{"\uEB39", []string{"yazi"}},                                            // U+EB39
		{"\u21AF", []string{"zig"}},                                             // U+21AF
	}
	for _, g := range groups {
		for _, p := range g.programs {
			builtinIcons[p] = g.glyph
		}
	}
}

// programIcon resolves a program's glyph: user map override, then the builtin
// table, then the configured fallback. An empty program yields "" on purpose --
// callers only prepend a non-empty glyph.
func programIcon(program string, icons *IconsConfig) string {
	if program == "" {
		return ""
	}
	if glyph, ok := icons.Map[program]; ok {
		return glyph
	}
	if glyph, ok := builtinIcons[program]; ok {
		return glyph
	}
	return icons.Fallback
}
