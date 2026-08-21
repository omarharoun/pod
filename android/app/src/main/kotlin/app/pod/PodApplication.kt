package app.pod

import android.app.Application
import app.pod.api.Copywriter
import dagger.hilt.android.HiltAndroidApp

@HiltAndroidApp
class PodApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        // A missing copy key is a packaging bug. Fail loudly in debug; render nothing
        // rather than a raw key in front of a person in release.
        Copywriter.DEBUG_STRICT = BuildConfig.DEBUG
    }
}
