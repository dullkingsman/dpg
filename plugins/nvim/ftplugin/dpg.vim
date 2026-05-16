if exists("b:did_ftplugin")
  finish
endif
let b:did_ftplugin = 1

setlocal commentstring=--%s
setlocal comments=:--
setlocal shiftwidth=4
setlocal expandtab
setlocal formatoptions-=t
setlocal iskeyword+=$
