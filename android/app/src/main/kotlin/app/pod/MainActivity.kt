package app.pod

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import app.pod.api.Copywriter
import app.pod.ui.theme.LocalCopywriter
import app.pod.ui.theme.PodTheme
import androidx.compose.runtime.CompositionLocalProvider
import dagger.hilt.android.AndroidEntryPoint
import javax.inject.Inject

@AndroidEntryPoint
class MainActivity : ComponentActivity() {

    @Inject lateinit var copy: Copywriter

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        setContent {
            PodTheme {
                CompositionLocalProvider(LocalCopywriter provides copy) {
                    RootScreen()
                }
            }
        }
    }
}
