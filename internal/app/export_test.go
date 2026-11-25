package app

// FindIndexBuilds is how often the finder's title index has been (re)built, for tests in app_test.
func (d *Deps) FindIndexBuilds() int64 { return d.findIdxBuilds.Load() }
