package app.pod.feature.auth

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import app.pod.api.ApiException
import app.pod.api.generated.CopyKey
import app.pod.api.generated.PodTokens
import app.pod.ui.components.PodNotice
import app.pod.ui.components.PodPrimaryButton
import app.pod.ui.theme.LocalCopywriter
import app.pod.ui.theme.LocalPodColors

/**
 * The first screen. One field, one button, and out.
 *
 * Variant B of two (see docs/DESIGN.md): the field takes focus on arrival and the IME's
 * Go key submits, so the screen is completable without ever reaching for the button.
 */
@Composable
fun SignInScreen(
    working: Boolean,
    failure: ApiException?,
    onSubmit: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val copy = LocalCopywriter.current
    val colors = LocalPodColors.current
    val keyboard = LocalSoftwareKeyboardController.current
    val focus = remember { FocusRequester() }
    var email by rememberSaveable { mutableStateOf("") }

    val canSubmit = email.contains('@') && !email.endsWith('@') && email.length > 3

    fun submit() {
        if (!canSubmit || working) return
        keyboard?.hide()
        onSubmit(email.trim())
    }

    LaunchedEffect(Unit) { focus.requestFocus() }

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
            copy(CopyKey.AUTH_START_TITLE),
            style = MaterialTheme.typography.titleLarge,
            color = colors.contentPrimary,
        )
        Text(
            copy(CopyKey.AUTH_START_BODY),
            style = MaterialTheme.typography.bodyLarge,
            color = colors.contentSecondary,
        )

        OutlinedTextField(
            value = email,
            onValueChange = { email = it },
            singleLine = true,
            label = { Text(copy(CopyKey.AUTH_START_EMAIL_LABEL)) },
            placeholder = { Text(copy(CopyKey.AUTH_START_EMAIL_PLACEHOLDER)) },
            keyboardOptions = KeyboardOptions(
                keyboardType = KeyboardType.Email,
                imeAction = ImeAction.Go,
            ),
            keyboardActions = KeyboardActions(onGo = { submit() }),
            modifier = Modifier
                .fillMaxWidth()
                .focusRequester(focus),
        )

        PodPrimaryButton(
            title = copy(CopyKey.AUTH_START_SUBMIT),
            onClick = ::submit,
            enabled = canSubmit,
            working = working,
        )

        if (failure != null) {
            PodNotice(failure = failure, onRetry = ::submit)
        }

        Text(
            copy(CopyKey.AUTH_START_LEGAL),
            style = MaterialTheme.typography.labelSmall,
            color = colors.contentTertiary,
        )
    }
}
