package app.pod.di

import android.content.Context
import app.pod.BuildConfig
import app.pod.api.Copywriter
import app.pod.api.OnboardingRepository
import app.pod.api.PodApiClient
import app.pod.api.SessionRepository
import app.pod.api.TokenStore
import app.pod.data.EncryptedTokenStore
import dagger.Module
import dagger.Provides
import dagger.hilt.InstallIn
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import javax.inject.Singleton

@Module
@InstallIn(SingletonComponent::class)
object AppModule {

    @Provides
    @Singleton
    fun tokenStore(@ApplicationContext context: Context): TokenStore = EncryptedTokenStore(context)

    @Provides
    @Singleton
    fun apiClient(tokens: TokenStore): PodApiClient =
        PodApiClient(baseUrl = BuildConfig.API_BASE_URL, tokens = tokens)

    @Provides
    @Singleton
    fun copywriter(): Copywriter = Copywriter.bundled

    /**
     * One repository for the whole process: the sign-in state machine must not fork when
     * the activity is recreated.
     */
    @Provides
    @Singleton
    fun sessionRepository(client: PodApiClient): SessionRepository =
        SessionRepository(client, deviceName = android.os.Build.MODEL)

    /**
     * One repository for the whole process: a configuration change must not discard a
     * half-answered intent or restart the flow.
     */
    @Provides
    @Singleton
    fun onboardingRepository(client: PodApiClient): OnboardingRepository =
        OnboardingRepository(client)
}
