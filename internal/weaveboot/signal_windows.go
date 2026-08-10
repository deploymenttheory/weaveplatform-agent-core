package weaveboot

import "os"

// terminateSignal on Windows: there is no graceful process signal, so this
// is os.Kill (TerminateProcess). Windows service stop semantics deliver
// graceful shutdown through the SCM, not this path.
var terminateSignal os.Signal = os.Kill
