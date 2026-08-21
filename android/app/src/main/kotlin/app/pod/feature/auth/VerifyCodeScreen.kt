package app.pod.feature.auth

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.weight
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import app.pod.api.ApiException
import app.pod.api.generated.CopyKey
import app.pod.api.generated.PodTokens
import app.pod.ui.components.PodNotice
import app.pod.ui.theme.LocalCopywriter
import app.pod.ui.theme.LocalPodColors
import kotlin.math.ceil
import kotlin.math.max
import kotlinx.coroutines.delay

private const val CODE_LENGTH = 6

/**
 * The code screen. It submits itself the moment six digits are in — nobody should have to
 * type a code they just read and then go looking for a button (CLAUDE.md §2 rule 7).
 */
@Composable
fun VerifyCodeScreen(
    email: String,
    expiresAtMillis: Long,
    resendAfterSeconds: Int,
    working: Boolean,
    failure: ApiException?,
    onSubmit: (String) -> Unit,
    onResend: () -> Unit,
    onChangeEmail: () -> Unit,
    modifier: Modifier = Modifier,
    nowMillis: () -> Long = System::currentTimeMillis,
) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current
    val focus = remember { FocusRequester() }
    // rememberSaveable, not remember: rotating the phone used to clear the typed code and
    // restart the resend countdown, which reads as the app having lost the code.
    var code by rememberSaveable { mutableStateOf("") }
    var secondsUntilResend by rememberSaveable { mutableIntStateOf(resendAfterSeconds) }

    // Rounded up, and never below one: "0 minutes" is not a thing to tell someone.
    val minutesRemaining = max(1, ceil((expiresAtMillis - nowMillis()) / 60_000.0).toInt())

    LaunchedEffect(Unit) { focus.requestFocus() }
    LaunchedEffect(secondsUntilResend) {
        if (secondsUntilResend > 0) {
            delay(1_000)
            secondsUntilResend -= 1
        }
    }

    Column(
        verticalArrangement = Arrangement.spacedBy(PodTokens.Spacing.LG.dp),
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .imePadding()
            .padding(
                horizontal = PodTokens.Spacing.SCREEN_GUTTER.dp,
                vertical = PodTokens.Spacing.XXXL.dp,
            ),
    ) {
        Text(
            copy(CopyKey.AUTH_VERIFY_TITLE),
            style = MaterialTheme.typography.titleLarge,
            color = colors.contentPrimary,
        )
        Text(
            copy(
                CopyKey.AUTH_VERIFY_BODY,
                mapOf("email" to email, "minutes" to minutesRemaining.toString()),
            ),
            style = MaterialTheme.typography.bodyLarge,
            color = colors.contentSecondary,
        )

        OutlinedTextField(
            value = code,
            onValueChange = { entered ->
                val digits = entered.filter(Char::isDigit).take(CODE_LENGTH)
                code = digits
                if (digits.length == CODE_LENGTH && !working) onSubmit(digits)
            },
            singleLine = true,
            label = { Text(copy(CopyKey.AUTH_VERIFY_CODE_LABEL)) },
            textStyle = MaterialTheme.typography.displaySmall,
            // Number, not NumberPassword. The code arrives by email (ADR-002), so the
            // person is reading it off one screen and typing it into another — masking
            // the digits hides the one thing they need to check.
            keyboardOptions = KeyboardOptions(
                keyboardType = KeyboardType.Number,
                imeAction = ImeAction.Done,
            ),
            keyboardActions = KeyboardActions(
                onDone = { if (code.length == CODE_LENGTH && !working) onSubmit(code) },
            ),
            modifier = Modifier
                .fillMaxWidth()
                .focusRequester(focus)
                .semantics { contentDescription = copy(CopyKey.A11Y_CODE_FIELD) },
        )

        if (failure != null) {
            PodNotice(failure = failure)
        }

        Row(modifier = Modifier.fillMaxWidth()) {
            if (secondsUntilResend > 0) {
                Text(
                    copy(
                        CopyKey.AUTH_VERIFY_RESEND_COUNTDOWN,
                        mapOf("seconds" to secondsUntilResend.toString()),
                    ),
                    style = MaterialTheme.typography.bodySmall,
                    color = colors.contentTertiary,
                )
            } else {
                TextButton(onClick = {
                    code = ""
                    secondsUntilResend = resendAfterSeconds
                    onResend()
                }) {
                    Text(copy(CopyKey.AUTH_VERIFY_RESEND), style = MaterialTheme.typography.bodySmall)
                }
            }
            Spacer(Modifier.weight(1f))
            TextButton(onClick = onChangeEmail) {
                Text(copy(CopyKey.AUTH_VERIFY_CHANGE_EMAIL), style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}
