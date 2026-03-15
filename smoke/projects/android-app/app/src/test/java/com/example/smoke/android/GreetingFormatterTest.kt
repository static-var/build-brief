package com.example.smoke.android

import org.junit.Assert.assertEquals
import org.junit.Test

class GreetingFormatterTest {
    @Test
    fun messageUsesProvidedName() {
        assertEquals("hello, android", GreetingFormatter.message("android"))
    }
}
